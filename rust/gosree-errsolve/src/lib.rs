#![forbid(unsafe_code)]

//! `gosree-errsolve` — Abstract-interpretation CFG/SSA dataflow solver for GoSree's
//! error-discipline rule family (`ERR001`–`ERR005`).
//!
//! Models Go `(T, error)` product returns as virtual Rust `#[must_use] Result<T, E>` sum
//! types over SSA control-flow graphs:
//! - `ERR001`: Unhandled `#[must_use]` error return (discarded call statement / go / defer).
//! - `ERR002`: Companion value unwrapped/used when `err` was blanked (`_`) or not proven `nil`.
//! - `ERR003`: Shadowed `err :=`, overwritten unread `err`, or `nilerr` (`if err != nil { return nil }`).
//! - `ERR004`: Typed-nil concrete pointer (`*T` implementing `error`) boxed into `error` interface.
//! - `ERR005`: Swallowed `Close()` error on a writable or written-to resource.
//!
//! Wire format: little-endian `GSE1` request frame -> `GSR1` response frame.

use std::cmp::Reverse;
use std::collections::{BinaryHeap, VecDeque};
use std::fmt;

pub const REQ_MAGIC: [u8; 4] = *b"GSE1";
pub const RESP_MAGIC: [u8; 4] = *b"GSR1";

pub const NONE_U32: u32 = u32::MAX;

/// Functions in one package frame. Every per-package structure is linear in this count (summary
/// bits, caller lists, the call-graph SCC arrays, the phase-1 worklists), and phase 1 costs
/// O((functions + call edges) x log functions) plus one solve per function and per callee flip
/// inside a call-graph cycle.
///
/// History: the bound is set by real packages (a package with its tests has reached 18,973:
/// ScholarLM `internal/wisdev`). Phase 1 used to visit dirty functions in index order, so a
/// function calling every member of a caller-first chain was solved about once per member, which
/// took 186 s at this limit (docs/ERROR_DISCIPLINE.md). Since 2026-11-12 phase 1 runs the call
/// graph's strongly connected components callees first, so that function is solved once, and
/// `PHASE1_WORK_FACTOR` bounds what a cycle can still cost. `MaxFuncs` in
/// `internal/gusseterr/wire.go` is the mirror.
pub const MAX_FUNCS: usize = 262_144;
pub const MAX_SLOTS: usize = 262_144;
pub const MAX_TUPLES: usize = 16_384;
pub const MAX_BLOCKS: usize = 32_768;
pub const MAX_PHIS_PER_BLOCK: usize = 16_384;
pub const MAX_PHI_ARITY: usize = 4_096;
pub const MAX_OPS_PER_BLOCK: usize = 262_144;
pub const MAX_COMPANIONS: usize = 256;
pub const MAX_ITERATIONS_PER_FUNC: usize = 250_000;

/// Per-function solver-state budget: `num_blocks * num_slots` may not exceed this.
///
/// The solver keeps four full abstract states per block (entry, exit and up to two outgoing
/// edges), each `num_slots` cells of `size_of::<SlotCell>()` = 28 bytes, so a function costs at
/// most `4 * 28 = 112` bytes per (block, slot) cell. `2^23 = 8,388,608` cells is therefore at
/// most 896 MiB of Rust solver state for one function (0.875 GiB); the Go oracle measured
/// 1.3 to 1.6 GiB at the budget, so the 1 GiB target holds for this engine only. States are allocated per function and
/// dropped before the next function is solved, so the peak is the largest function, not the
/// sum over functions. `MaxStateCells` in `internal/gusseterr/wire.go` is the mirror.
pub const MAX_STATE_CELLS_PER_FUNC: u64 = 8_388_608;

/// Largest `GSE1` request frame either engine accepts: 1 GiB, gusset's `MaxBufferBytes`, the
/// largest payload the linked engine can be handed at all. The pure-Go oracle enforces the same
/// bound so both builds refuse the same frames. `MaxFrameBytes` in `internal/gusseterr/wire.go`
/// is the mirror.
pub const MAX_FRAME_BYTES: usize = 1 << 30;

/// Largest decoded request this decoder will build, in the bytes the cost model below charges
/// before each structure is allocated: 2 GiB, twice `MAX_FRAME_BYTES`. A frame byte can decode
/// to many more bytes (a 5-byte op decodes to a `DECODED_OP_BYTES` record, a 9-byte empty block
/// to `DECODED_BLOCK_BYTES`), so the frame bound alone does not bound memory: a 100 MiB frame
/// of ops decoded to 1,139 MiB in the Go oracle. The worst case is 11.2 decoded bytes per frame
/// byte, so a densely packed frame over about 190 MiB is refused and any frame of 1 GiB that
/// is mostly slot flags or phi edges is accepted. `MaxDecodedBytes` in
/// `internal/gusseterr/wire.go` is the mirror.
pub const MAX_DECODED_BYTES: u64 = 1 << 31;

/// Decoded-size cost model, mirrored by the `Decoded*Bytes` constants in
/// `internal/gusseterr/wire.go`. Each is at least the in-memory size of the record in both
/// engines (asserted by tests): the Go record is the larger one, so the model charges Go's sizes.
pub const DECODED_FUNC_BYTES: u64 = 96;
pub const DECODED_TUPLE_BYTES: u64 = 12;
pub const DECODED_BLOCK_BYTES: u64 = 96;
pub const DECODED_PHI_BYTES: u64 = 32;
pub const DECODED_PHI_EDGE_BYTES: u64 = 8;
pub const DECODED_OP_BYTES: u64 = 56;
pub const DECODED_COMPANION_BYTES: u64 = 4;

/// Fewest wire bytes of each record a count in the frame can claim: a function header, a tuple, a
/// block (terminator tag, phi count, op count), a phi (slot and arity), a phi edge, an op (kind
/// and site id) and a companion slot.
const MIN_FUNC_WIRE_BYTES: usize = 13;
const TUPLE_WIRE_BYTES: usize = 9;
const MIN_BLOCK_WIRE_BYTES: usize = 9;
const MIN_PHI_WIRE_BYTES: usize = 6;
const PHI_EDGE_WIRE_BYTES: usize = 8;
const MIN_OP_WIRE_BYTES: usize = 5;
const COMPANION_WIRE_BYTES: usize = 4;

/// Largest `GSR1` response either engine returns: 1 GiB, gusset's `MAX_BUFFER_BYTES` (the
/// native path refuses a larger engine output with `output N bytes exceeds maximum M bytes`).
/// The solver refuses before encoding, with the same text, and the Go oracle does the same.
/// `MaxResponseBytes` in `internal/gusseterr/wire.go` is the mirror.
pub const MAX_RESPONSE_BYTES: usize = 1 << 30;

/// Phase-1 work budget: the solves of the interprocedural summary phase may do at most
/// `PHASE1_WORK_FACTOR` x the package's size in work units, and at least `PHASE1_WORK_FLOOR`.
/// A function's work unit count is its ops plus its state cells, blocks x max(slots, 1) (the
/// product MAX_STATE_CELLS_PER_FUNC bounds); a solve of it costs one count.
/// The unit is a property of the package, not of the machine, so both engines (and every
/// load) refuse exactly the same packages. See docs/ERROR_DISCIPLINE.md for the measured
/// ratios the factor was chosen from.
pub const PHASE1_WORK_FACTOR: u64 = 64;
pub const PHASE1_WORK_FLOOR: u64 = 1 << 20;

// Function flags.
pub const FUNC_RETURNS_ERROR: u8 = 1 << 0;
pub const FUNC_RETURNS_CONCRETE_ERR_PTR: u8 = 1 << 1;
pub const FUNC_EXTERNAL_MAY_NIL: u8 = 1 << 2;

// Slot flags.
pub const SLOT_IS_ERROR: u8 = 1 << 0;
pub const SLOT_INIT_MAY_NIL_PARAM: u8 = 1 << 1;
pub const SLOT_INIT_WRITABLE: u8 = 1 << 2;

// Tuple flags.
pub const TUPLE_ERR_BLANKED: u8 = 1 << 0;
pub const TUPLE_IO_READ: u8 = 1 << 1;

// Terminator kinds.
pub const TERM_JUMP: u8 = 0;
pub const TERM_COND: u8 = 1;
pub const TERM_RETURN: u8 = 2;
pub const TERM_STOP: u8 = 3;

// Branch condition kinds.
pub const COND_OPAQUE: u8 = 0;
pub const COND_IS_NIL: u8 = 1;
pub const COND_NOT_NIL: u8 = 2;
pub const COND_GT_ZERO: u8 = 3;

// Return flags.
pub const RET_LITERAL_NIL_ERR: u8 = 1 << 0;
pub const RET_CONCRETE_LITERAL_NIL: u8 = 1 << 1;

// Operation kinds.
pub const OP_UNHANDLED_ERR: u8 = 1;
pub const OP_BIND_TUPLE: u8 = 2;
pub const OP_BIND_ERR: u8 = 3;
pub const OP_ASSIGN_SLOT: u8 = 4;
pub const OP_CALL_CONCRETE_PTR: u8 = 5;
pub const OP_OBSERVE_ERR: u8 = 6;
pub const OP_USE_COMPANION: u8 = 7;
pub const OP_SHADOW_ERR: u8 = 8;
pub const OP_BOX_CONCRETE_ERR: u8 = 9;
pub const OP_MARK_WRITABLE: u8 = 10;
pub const OP_UNHANDLED_CLOSE: u8 = 11;

// Nil assignment hints for OP_ASSIGN_SLOT.
pub const ASSIGN_NIL_COPY: u8 = 0;
pub const ASSIGN_NIL_NON_NIL: u8 = 1;
pub const ASSIGN_NIL_MAY_NIL: u8 = 2;
pub const ASSIGN_NIL_MUST_NIL: u8 = 3;

// Rule codes.
pub const RULE_ERR001: u8 = 1;
pub const RULE_ERR002: u8 = 2;
pub const RULE_ERR003: u8 = 3;
pub const RULE_ERR004: u8 = 4;
pub const RULE_ERR005: u8 = 5;

// Diagnostic sub-kinds.
pub const SUB_DEFAULT: u8 = 0;
pub const SUB_ERR002_UNCHECKED: u8 = 0;
pub const SUB_ERR002_BLANKED: u8 = 1;
pub const SUB_ERR003_SHADOWED: u8 = 1;
pub const SUB_ERR003_OVERWRITTEN: u8 = 2;
pub const SUB_ERR003_NILERR: u8 = 3;
pub const SUB_ERR004_MAY_NIL: u8 = 0;
pub const SUB_ERR004_MUST_NIL: u8 = 1;
pub const SUB_ERR005_SYNC_CLOSE: u8 = 0;
pub const SUB_ERR005_DEFER_CLOSE: u8 = 1;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum SolveError {
    BadMagic,
    Truncated,
    TrailingBytes(usize),
    LimitExceeded(&'static str),
    InvalidEnum(&'static str, u8),
    IndexOutOfBounds(&'static str, u32, u32),
    IterationBudgetExceeded(u32),
    /// The interprocedural summary fixpoint did not converge within the iteration cap.
    CallgraphBudgetExceeded(usize),
    /// A solver-state allocation failed (reported instead of aborting the process).
    AllocationFailed(&'static str),
    /// Phase 1 did more work units (`.0`) than the package's budget (`.1`) allows.
    WorkBudgetExceeded(u64, u64),
    /// The encoded response would be `.0` bytes, over the maximum `.1`.
    ResponseTooLarge(usize, usize),
    Cancelled,
}

impl fmt::Display for SolveError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SolveError::BadMagic => write!(f, "invalid GSE1 frame magic"),
            SolveError::Truncated => write!(f, "truncated GSE1 frame"),
            SolveError::TrailingBytes(n) => write!(f, "GSE1 frame has {n} trailing bytes"),
            SolveError::LimitExceeded(what) => match limit_bound(what) {
                Some(bound) => write!(f, "GSE1 limit exceeded: {what} over the bound of {bound}"),
                None => write!(f, "GSE1 limit exceeded: {what}"),
            },
            SolveError::InvalidEnum(what, v) => write!(f, "invalid {what} tag: {v}"),
            SolveError::IndexOutOfBounds(what, idx, max) => {
                write!(f, "{what} index {idx} out of bounds (len {max})")
            }
            SolveError::IterationBudgetExceeded(fn_id) => {
                write!(f, "CFG worklist iteration budget exceeded in func {fn_id} (bound {MAX_ITERATIONS_PER_FUNC} steps)")
            }
            SolveError::CallgraphBudgetExceeded(iters) => write!(
                f,
                "call-graph summary fixpoint did not converge within {iters} iterations"
            ),
            SolveError::AllocationFailed(what) => {
                write!(f, "solver allocation failed: {what}")
            }
            SolveError::WorkBudgetExceeded(work, budget) => write!(
                f,
                "phase-1 work budget exceeded: {work} work units against a budget of {budget} (ops plus blocks x max(slots, 1) of every function solved; the budget is {PHASE1_WORK_FACTOR} times the package's total of that, at least {PHASE1_WORK_FLOOR})"
            ),
            SolveError::ResponseTooLarge(bytes, max) => {
                write!(f, "output {bytes} bytes exceeds maximum {max} bytes")
            }
            SolveError::Cancelled => write!(f, "solver cancelled"),
        }
    }
}

impl std::error::Error for SolveError {}

/// The bound a `LimitExceeded` names, so every limit message says what it was refused against.
/// Mirrored by `limitBound` in `internal/gusseterr/wire.go`.
fn limit_bound(what: &str) -> Option<u64> {
    Some(match what {
        "frame_bytes" => MAX_FRAME_BYTES as u64,
        "decoded_bytes" => MAX_DECODED_BYTES,
        "num_funcs" => MAX_FUNCS as u64,
        "num_slots" => MAX_SLOTS as u64,
        "num_tuples" => MAX_TUPLES as u64,
        "num_blocks" => MAX_BLOCKS as u64,
        "state cells (blocks x slots)" => MAX_STATE_CELLS_PER_FUNC,
        "num_phis" => MAX_PHIS_PER_BLOCK as u64,
        "phi_arity" => MAX_PHI_ARITY as u64,
        "num_ops" => MAX_OPS_PER_BLOCK as u64,
        "companions" => MAX_COMPANIONS as u64,
        _ => return None,
    })
}

impl SolveError {
    /// A stable machine-readable name of the refusal, for the engine's wire message.
    pub fn code(&self) -> &'static str {
        match self {
            SolveError::BadMagic => "bad_magic",
            SolveError::Truncated => "truncated",
            SolveError::TrailingBytes(_) => "trailing_bytes",
            SolveError::LimitExceeded(what) if *what == "decoded_bytes" => "decoded_bytes_exceeded",
            SolveError::LimitExceeded(_) => "limit_exceeded",
            SolveError::InvalidEnum(_, _) => "invalid_enum",
            SolveError::IndexOutOfBounds(_, _, _) => "index_out_of_bounds",
            SolveError::IterationBudgetExceeded(_) => "iteration_budget_exceeded",
            SolveError::CallgraphBudgetExceeded(_) => "callgraph_budget_exceeded",
            SolveError::AllocationFailed(_) => "allocation_failed",
            SolveError::WorkBudgetExceeded(_, _) => "work_budget_exceeded",
            SolveError::ResponseTooLarge(_, _) => "response_too_large",
            SolveError::Cancelled => "cancelled",
        }
    }

    /// The message the engine hands gusset: `<code>: <text>`. The Go side maps the code to its
    /// sentinel errors and prints the text, so both engines read the same.
    pub fn wire_message(&self) -> String {
        format!("{}: {}", self.code(), self)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct Finding {
    pub func_idx: u32,
    pub site_id: u32,
    pub rule_code: u8,
    pub sub_kind: u8,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct PackageResult {
    pub may_return_nil: Vec<bool>,
    pub findings: Vec<Finding>,
}

#[derive(Debug, Clone)]
pub struct TupleMeta {
    pub err_slot: u32,
    pub call_site_id: u32,
    pub flags: u8,
}

#[derive(Debug, Clone)]
pub struct PhiIR {
    pub dst_slot: u32,
    pub incoming: Vec<(u32, u32)>, // (pred_bb, src_slot)
}

#[derive(Debug, Clone)]
pub enum Terminator {
    Jump {
        target: u32,
    },
    Cond {
        true_bb: u32,
        false_bb: u32,
        cond_kind: u8,
        cond_slot: u32,
    },
    Return {
        site_id: u32,
        ret_slot: u32,
        flags: u8,
    },
    Stop,
}

#[derive(Debug, Clone)]
pub enum OpIR {
    UnhandledErr {
        site_id: u32,
    },
    BindTuple {
        site_id: u32,
        tuple_id: u32,
        err_slot: u32,
        companions: Vec<u32>,
    },
    BindErr {
        site_id: u32,
        err_slot: u32,
    },
    AssignSlot {
        site_id: u32,
        dst_slot: u32,
        src_slot: u32,
        nil_hint: u8,
        writable_hint: u8,
    },
    CallConcretePtr {
        site_id: u32,
        dst_slot: u32,
        callee_idx: u32,
    },
    ObserveErr {
        site_id: u32,
        slot: u32,
    },
    UseCompanion {
        site_id: u32,
        slot: u32,
    },
    ShadowErr {
        site_id: u32,
        inner_slot: u32,
        outer_slot: u32,
    },
    BoxConcreteErr {
        site_id: u32,
        ptr_slot: u32,
    },
    MarkWritable {
        site_id: u32,
        res_slot: u32,
    },
    UnhandledClose {
        site_id: u32,
        res_slot: u32,
        is_defer: bool,
    },
}

#[derive(Debug, Clone)]
pub struct BlockIR {
    pub phis: Vec<PhiIR>,
    pub ops: Vec<OpIR>,
    pub term: Terminator,
}

#[derive(Debug, Clone)]
pub struct FuncIR {
    pub flags: u8,
    pub slot_flags: Vec<u8>,
    pub tuples: Vec<TupleMeta>,
    pub blocks: Vec<BlockIR>,
}

struct Cursor<'a> {
    buf: &'a [u8],
    pos: usize,
}

impl<'a> Cursor<'a> {
    fn new(buf: &'a [u8]) -> Self {
        Self { buf, pos: 0 }
    }

    fn remaining(&self) -> usize {
        self.buf.len().saturating_sub(self.pos)
    }

    /// Fails with `Truncated` unless `count` records of at least `unit` wire bytes each could
    /// still be in the frame: the decoder allocates a collection at exactly its claimed count,
    /// so a count the remaining bytes cannot back is refused before anything is allocated.
    fn need(&self, count: usize, unit: usize) -> Result<(), SolveError> {
        if self.remaining() < count.saturating_mul(unit) {
            return Err(SolveError::Truncated);
        }
        Ok(())
    }

    fn read_exact(&mut self, n: usize) -> Result<&'a [u8], SolveError> {
        let end = self.pos.checked_add(n).ok_or(SolveError::Truncated)?;
        if end > self.buf.len() {
            return Err(SolveError::Truncated);
        }
        let s = &self.buf[self.pos..end];
        self.pos = end;
        Ok(s)
    }

    fn read_u8(&mut self) -> Result<u8, SolveError> {
        Ok(self.read_exact(1)?[0])
    }

    fn read_u16(&mut self) -> Result<u16, SolveError> {
        let b = self.read_exact(2)?;
        Ok(u16::from_le_bytes([b[0], b[1]]))
    }

    fn read_u32(&mut self) -> Result<u32, SolveError> {
        let b = self.read_exact(4)?;
        Ok(u32::from_le_bytes([b[0], b[1], b[2], b[3]]))
    }
}

fn check_slot(slot: u32, num_slots: u32, allow_none: bool) -> Result<(), SolveError> {
    if allow_none && slot == NONE_U32 {
        return Ok(());
    }
    if slot >= num_slots {
        return Err(SolveError::IndexOutOfBounds("slot", slot, num_slots));
    }
    Ok(())
}

fn check_block(bb: u32, num_blocks: u32) -> Result<(), SolveError> {
    if bb >= num_blocks {
        return Err(SolveError::IndexOutOfBounds("block", bb, num_blocks));
    }
    Ok(())
}

/// Charges decoded bytes against the decoded-size bound before the structure they pay for is
/// allocated, so a hostile count is refused before it is acted on.
struct DecodeBudget {
    used: u64,
    max: u64,
}

impl DecodeBudget {
    fn charge(&mut self, count: u64, unit: u64) -> Result<(), SolveError> {
        let total = count
            .checked_mul(unit)
            .and_then(|c| c.checked_add(self.used))
            .filter(|&t| t <= self.max)
            .ok_or(SolveError::LimitExceeded("decoded_bytes"))?;
        self.used = total;
        Ok(())
    }
}

/// The bytes the decoder charges for `funcs`, by the same cost model it charges while
/// decoding: `decode_package_bounded` accepts a frame with this bound and refuses it with one
/// byte less.
pub fn decoded_bytes(funcs: &[FuncIR]) -> u64 {
    let mut total = 0u64;
    for f in funcs {
        total += DECODED_FUNC_BYTES
            + f.slot_flags.len() as u64
            + f.tuples.len() as u64 * DECODED_TUPLE_BYTES;
        for b in &f.blocks {
            total += DECODED_BLOCK_BYTES + b.phis.len() as u64 * DECODED_PHI_BYTES;
            for phi in &b.phis {
                total += phi.incoming.len() as u64 * DECODED_PHI_EDGE_BYTES;
            }
            total += b.ops.len() as u64 * DECODED_OP_BYTES;
            for op in &b.ops {
                if let OpIR::BindTuple { companions, .. } = op {
                    total += companions.len() as u64 * DECODED_COMPANION_BYTES;
                }
            }
        }
    }
    total
}

/// Decodes a `GSE1` binary request frame into validated `FuncIR` records.
pub fn decode_package(frame: &[u8]) -> Result<Vec<FuncIR>, SolveError> {
    decode_package_bounded(frame, MAX_DECODED_BYTES)
}

/// `decode_package` with an explicit decoded-size bound (the public entry point passes
/// `MAX_DECODED_BYTES`; tests pass a small one to reach the limit).
pub fn decode_package_bounded(
    frame: &[u8],
    max_decoded_bytes: u64,
) -> Result<Vec<FuncIR>, SolveError> {
    let mut budget = DecodeBudget {
        used: 0,
        max: max_decoded_bytes,
    };
    if frame.len() > MAX_FRAME_BYTES {
        return Err(SolveError::LimitExceeded("frame_bytes"));
    }
    let mut cur = Cursor::new(frame);
    let magic = cur.read_exact(4)?;
    if magic != REQ_MAGIC {
        return Err(SolveError::BadMagic);
    }
    let num_funcs_u32 = cur.read_u32()?;
    let num_funcs = num_funcs_u32 as usize;
    if num_funcs > MAX_FUNCS {
        return Err(SolveError::LimitExceeded("num_funcs"));
    }

    cur.need(num_funcs, MIN_FUNC_WIRE_BYTES)?;
    let mut funcs = Vec::with_capacity(num_funcs);
    for _ in 0..num_funcs {
        let flags = cur.read_u8()?;
        let num_slots_u32 = cur.read_u32()?;
        let num_slots = num_slots_u32 as usize;
        if num_slots > MAX_SLOTS {
            return Err(SolveError::LimitExceeded("num_slots"));
        }
        let num_tuples_u32 = cur.read_u32()?;
        let num_tuples = num_tuples_u32 as usize;
        if num_tuples > MAX_TUPLES {
            return Err(SolveError::LimitExceeded("num_tuples"));
        }
        let num_blocks_u32 = cur.read_u32()?;
        let num_blocks = num_blocks_u32 as usize;
        if num_blocks > MAX_BLOCKS {
            return Err(SolveError::LimitExceeded("num_blocks"));
        }
        if (num_blocks as u64) * (num_slots as u64) > MAX_STATE_CELLS_PER_FUNC {
            return Err(SolveError::LimitExceeded("state cells (blocks x slots)"));
        }
        budget.charge(1, DECODED_FUNC_BYTES)?;
        budget.charge(num_slots as u64, 1)?;
        budget.charge(num_tuples as u64, DECODED_TUPLE_BYTES)?;

        let slot_flags = cur.read_exact(num_slots)?.to_vec();

        cur.need(num_tuples, TUPLE_WIRE_BYTES)?;
        let mut tuples = Vec::with_capacity(num_tuples);
        for _ in 0..num_tuples {
            let err_slot = cur.read_u32()?;
            check_slot(err_slot, num_slots_u32, true)?;
            let call_site_id = cur.read_u32()?;
            let tflags = cur.read_u8()?;
            tuples.push(TupleMeta {
                err_slot,
                call_site_id,
                flags: tflags,
            });
        }

        cur.need(num_blocks, MIN_BLOCK_WIRE_BYTES)?;
        let mut blocks = Vec::with_capacity(num_blocks);
        for _ in 0..num_blocks {
            budget.charge(1, DECODED_BLOCK_BYTES)?;
            let term_kind = cur.read_u8()?;
            let term = match term_kind {
                TERM_JUMP => {
                    let target = cur.read_u32()?;
                    check_block(target, num_blocks_u32)?;
                    Terminator::Jump { target }
                }
                TERM_COND => {
                    let true_bb = cur.read_u32()?;
                    let false_bb = cur.read_u32()?;
                    check_block(true_bb, num_blocks_u32)?;
                    check_block(false_bb, num_blocks_u32)?;
                    let cond_kind = cur.read_u8()?;
                    if cond_kind > COND_GT_ZERO {
                        return Err(SolveError::InvalidEnum("cond_kind", cond_kind));
                    }
                    let cond_slot = cur.read_u32()?;
                    let allow_none = cond_kind == COND_OPAQUE;
                    check_slot(cond_slot, num_slots_u32, allow_none)?;
                    Terminator::Cond {
                        true_bb,
                        false_bb,
                        cond_kind,
                        cond_slot,
                    }
                }
                TERM_RETURN => {
                    let site_id = cur.read_u32()?;
                    let ret_slot = cur.read_u32()?;
                    check_slot(ret_slot, num_slots_u32, true)?;
                    let rflags = cur.read_u8()?;
                    Terminator::Return {
                        site_id,
                        ret_slot,
                        flags: rflags,
                    }
                }
                TERM_STOP => Terminator::Stop,
                other => return Err(SolveError::InvalidEnum("term_kind", other)),
            };

            let num_phis = cur.read_u32()? as usize;
            if num_phis > MAX_PHIS_PER_BLOCK {
                return Err(SolveError::LimitExceeded("num_phis"));
            }
            budget.charge(num_phis as u64, DECODED_PHI_BYTES)?;
            cur.need(num_phis, MIN_PHI_WIRE_BYTES)?;
            let mut phis = Vec::with_capacity(num_phis);
            for _ in 0..num_phis {
                let dst_slot = cur.read_u32()?;
                check_slot(dst_slot, num_slots_u32, false)?;
                let arity = cur.read_u16()? as usize;
                if arity > MAX_PHI_ARITY {
                    return Err(SolveError::LimitExceeded("phi_arity"));
                }
                budget.charge(arity as u64, DECODED_PHI_EDGE_BYTES)?;
                cur.need(arity, PHI_EDGE_WIRE_BYTES)?;
                let mut incoming = Vec::with_capacity(arity);
                for _ in 0..arity {
                    let pred_bb = cur.read_u32()?;
                    check_block(pred_bb, num_blocks_u32)?;
                    let src_slot = cur.read_u32()?;
                    check_slot(src_slot, num_slots_u32, false)?;
                    incoming.push((pred_bb, src_slot));
                }
                phis.push(PhiIR { dst_slot, incoming });
            }

            let num_ops = cur.read_u32()? as usize;
            if num_ops > MAX_OPS_PER_BLOCK {
                return Err(SolveError::LimitExceeded("num_ops"));
            }
            budget.charge(num_ops as u64, DECODED_OP_BYTES)?;
            cur.need(num_ops, MIN_OP_WIRE_BYTES)?;
            let mut ops = Vec::with_capacity(num_ops);
            for _ in 0..num_ops {
                let op_kind = cur.read_u8()?;
                let site_id = cur.read_u32()?;
                let op = match op_kind {
                    OP_UNHANDLED_ERR => OpIR::UnhandledErr { site_id },
                    OP_BIND_TUPLE => {
                        let tuple_id = cur.read_u32()?;
                        if tuple_id >= num_tuples_u32 {
                            return Err(SolveError::IndexOutOfBounds(
                                "tuple",
                                tuple_id,
                                num_tuples_u32,
                            ));
                        }
                        let err_slot = cur.read_u32()?;
                        check_slot(err_slot, num_slots_u32, true)?;
                        let comp_count = cur.read_u16()? as usize;
                        if comp_count > MAX_COMPANIONS {
                            return Err(SolveError::LimitExceeded("companions"));
                        }
                        budget.charge(comp_count as u64, DECODED_COMPANION_BYTES)?;
                        cur.need(comp_count, COMPANION_WIRE_BYTES)?;
                        let mut companions = Vec::with_capacity(comp_count);
                        for _ in 0..comp_count {
                            let cs = cur.read_u32()?;
                            check_slot(cs, num_slots_u32, false)?;
                            companions.push(cs);
                        }
                        OpIR::BindTuple {
                            site_id,
                            tuple_id,
                            err_slot,
                            companions,
                        }
                    }
                    OP_BIND_ERR => {
                        let err_slot = cur.read_u32()?;
                        check_slot(err_slot, num_slots_u32, false)?;
                        OpIR::BindErr { site_id, err_slot }
                    }
                    OP_ASSIGN_SLOT => {
                        let dst_slot = cur.read_u32()?;
                        check_slot(dst_slot, num_slots_u32, false)?;
                        let src_slot = cur.read_u32()?;
                        check_slot(src_slot, num_slots_u32, true)?;
                        let nil_hint = cur.read_u8()?;
                        if nil_hint > ASSIGN_NIL_MUST_NIL {
                            return Err(SolveError::InvalidEnum("nil_hint", nil_hint));
                        }
                        let writable_hint = cur.read_u8()?;
                        if writable_hint > 1 {
                            return Err(SolveError::InvalidEnum("writable_hint", writable_hint));
                        }
                        OpIR::AssignSlot {
                            site_id,
                            dst_slot,
                            src_slot,
                            nil_hint,
                            writable_hint,
                        }
                    }
                    OP_CALL_CONCRETE_PTR => {
                        let dst_slot = cur.read_u32()?;
                        check_slot(dst_slot, num_slots_u32, false)?;
                        let callee_idx = cur.read_u32()?;
                        if callee_idx >= num_funcs_u32 {
                            return Err(SolveError::IndexOutOfBounds(
                                "callee_func",
                                callee_idx,
                                num_funcs_u32,
                            ));
                        }
                        OpIR::CallConcretePtr {
                            site_id,
                            dst_slot,
                            callee_idx,
                        }
                    }
                    OP_OBSERVE_ERR => {
                        let slot = cur.read_u32()?;
                        check_slot(slot, num_slots_u32, false)?;
                        OpIR::ObserveErr { site_id, slot }
                    }
                    OP_USE_COMPANION => {
                        let slot = cur.read_u32()?;
                        check_slot(slot, num_slots_u32, false)?;
                        OpIR::UseCompanion { site_id, slot }
                    }
                    OP_SHADOW_ERR => {
                        let inner_slot = cur.read_u32()?;
                        let outer_slot = cur.read_u32()?;
                        check_slot(inner_slot, num_slots_u32, false)?;
                        check_slot(outer_slot, num_slots_u32, false)?;
                        OpIR::ShadowErr {
                            site_id,
                            inner_slot,
                            outer_slot,
                        }
                    }
                    OP_BOX_CONCRETE_ERR => {
                        let ptr_slot = cur.read_u32()?;
                        check_slot(ptr_slot, num_slots_u32, false)?;
                        OpIR::BoxConcreteErr { site_id, ptr_slot }
                    }
                    OP_MARK_WRITABLE => {
                        let res_slot = cur.read_u32()?;
                        check_slot(res_slot, num_slots_u32, false)?;
                        OpIR::MarkWritable { site_id, res_slot }
                    }
                    OP_UNHANDLED_CLOSE => {
                        let res_slot = cur.read_u32()?;
                        check_slot(res_slot, num_slots_u32, false)?;
                        let is_defer = cur.read_u8()?;
                        if is_defer > 1 {
                            return Err(SolveError::InvalidEnum("is_defer", is_defer));
                        }
                        OpIR::UnhandledClose {
                            site_id,
                            res_slot,
                            is_defer: is_defer != 0,
                        }
                    }
                    other => return Err(SolveError::InvalidEnum("op_kind", other)),
                };
                ops.push(op);
            }

            blocks.push(BlockIR { phis, ops, term });
        }

        funcs.push(FuncIR {
            flags,
            slot_flags,
            tuples,
            blocks,
        });
    }

    if cur.remaining() != 0 {
        return Err(SolveError::TrailingBytes(cur.remaining()));
    }
    Ok(funcs)
}

/// Length in bytes of the `GSR1` frame `encode_result` produces for `res`.
pub fn encoded_result_len(res: &PackageResult) -> usize {
    4 + 4 + res.may_return_nil.len() + 4 + res.findings.len().saturating_mul(12)
}

/// Encodes a `PackageResult` into a deterministic `GSR1` binary response frame.
pub fn encode_result(res: &PackageResult) -> Vec<u8> {
    let mut out = Vec::with_capacity(encoded_result_len(res));
    out.extend_from_slice(&RESP_MAGIC);
    out.extend_from_slice(&(res.may_return_nil.len() as u32).to_le_bytes());
    for &b in &res.may_return_nil {
        out.push(u8::from(b));
    }
    out.extend_from_slice(&(res.findings.len() as u32).to_le_bytes());
    for f in &res.findings {
        out.extend_from_slice(&f.func_idx.to_le_bytes());
        out.extend_from_slice(&f.site_id.to_le_bytes());
        out.push(f.rule_code);
        out.push(f.sub_kind);
        out.extend_from_slice(&0u16.to_le_bytes());
    }
    out
}

/// Solves a `GSE1` binary request frame and returns the `GSR1` binary response frame.
pub fn solve_frame<F>(frame: &[u8], check_cancel: F) -> Result<Vec<u8>, SolveError>
where
    F: FnMut() -> bool,
{
    solve_frame_bounded(frame, check_cancel, MAX_RESPONSE_BYTES)
}

/// `solve_frame` with an explicit response-size bound (tests pass a small one). The response
/// length is known from the result before anything is encoded, so an over-large one is refused
/// without allocating it.
fn solve_frame_bounded<F>(
    frame: &[u8],
    mut check_cancel: F,
    max_response_bytes: usize,
) -> Result<Vec<u8>, SolveError>
where
    F: FnMut() -> bool,
{
    let funcs = decode_package(frame)?;
    let res = solve_package(&funcs, &mut check_cancel)?;
    let len = encoded_result_len(&res);
    if len > max_response_bytes {
        return Err(SolveError::ResponseTooLarge(len, max_response_bytes));
    }
    Ok(encode_result(&res))
}

// ============================================================================
// Abstract Domains & Join-Semilattice Operations
// ============================================================================

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Origin {
    Bot,
    Safe,
    Single(u32),
    UncheckedMix(u32),
    Blanked(u32),
}

impl Origin {
    fn join(self, other: Self) -> Self {
        use Origin::*;
        if self == other {
            return self;
        }
        match (self, other) {
            (Bot, x) | (x, Bot) => x,
            (Blanked(a), Blanked(b)) => Blanked(a.min(b)),
            (Blanked(a), _) | (_, Blanked(a)) => Blanked(a),
            (UncheckedMix(a), UncheckedMix(b)) => UncheckedMix(a.min(b)),
            (UncheckedMix(a), Single(b)) | (Single(b), UncheckedMix(a)) => UncheckedMix(a.min(b)),
            (UncheckedMix(a), Safe) | (Safe, UncheckedMix(a)) => UncheckedMix(a),
            (Single(a), Single(b)) => UncheckedMix(a.min(b)),
            (Single(a), Safe) | (Safe, Single(a)) => UncheckedMix(a),
            (Safe, Safe) => Safe,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum NilVal {
    Bot,
    MustNil,
    NonNilChecked,
    NonNil,
    MayNil,
}

impl NilVal {
    fn join(self, other: Self) -> Self {
        use NilVal::*;
        if self == other {
            return self;
        }
        match (self, other) {
            (Bot, x) | (x, Bot) => x,
            (NonNilChecked, NonNil) | (NonNil, NonNilChecked) => NonNil,
            _ => MayNil,
        }
    }

    fn relax_checked(self) -> Self {
        if self == NilVal::NonNilChecked {
            NilVal::NonNil
        } else {
            self
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ErrVar {
    Bot,
    Clean,
    Unread(u32),
}

impl ErrVar {
    fn join(self, other: Self) -> Self {
        use ErrVar::*;
        match (self, other) {
            (Bot, x) | (x, Bot) => x,
            (Clean, Clean) => Clean,
            (Unread(a), Unread(b)) => Unread(a.min(b)),
            (Unread(a), Clean) | (Clean, Unread(a)) => Unread(a),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum WriteMode {
    Bot,
    ReadOnly,
    Writable,
}

impl WriteMode {
    fn join(self, other: Self) -> Self {
        match (self, other) {
            (WriteMode::Bot, x) | (x, WriteMode::Bot) => x,
            (WriteMode::Writable, _) | (_, WriteMode::Writable) => WriteMode::Writable,
            (WriteMode::ReadOnly, WriteMode::ReadOnly) => WriteMode::ReadOnly,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ErrLink {
    Bot,
    Tuple(u32),
    None,
}

impl ErrLink {
    fn join(self, other: Self) -> Self {
        if self == other {
            return self;
        }
        match (self, other) {
            (ErrLink::Bot, x) | (x, ErrLink::Bot) => x,
            _ => ErrLink::None,
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
struct SlotCell {
    origin: Origin,
    nil: NilVal,
    err_var: ErrVar,
    writable: WriteMode,
    err_link: ErrLink,
}

impl SlotCell {
    const BOT: Self = Self {
        origin: Origin::Bot,
        nil: NilVal::Bot,
        err_var: ErrVar::Bot,
        writable: WriteMode::Bot,
        err_link: ErrLink::Bot,
    };

    #[inline]
    fn join(self, other: Self) -> Self {
        Self {
            origin: self.origin.join(other.origin),
            nil: self.nil.join(other.nil),
            err_var: self.err_var.join(other.err_var),
            writable: self.writable.join(other.writable),
            err_link: self.err_link.join(other.err_link),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct State {
    reachable: bool,
    cells: Vec<SlotCell>,
}

/// Allocates `n` elements without aborting the process when the allocator refuses.
fn try_vec<T>(n: usize, what: &'static str) -> Result<Vec<T>, SolveError> {
    let mut v = Vec::new();
    v.try_reserve_exact(n)
        .map_err(|_| SolveError::AllocationFailed(what))?;
    Ok(v)
}

impl State {
    fn try_bottom(num_slots: usize) -> Result<Self, SolveError> {
        let mut cells = try_vec(num_slots, "block state")?;
        cells.resize(num_slots, SlotCell::BOT);
        Ok(Self {
            reachable: false,
            cells,
        })
    }

    fn try_entry(slot_flags: &[u8]) -> Result<Self, SolveError> {
        let mut cells = try_vec(slot_flags.len(), "entry state")?;
        for &sf in slot_flags {
            cells.push(SlotCell {
                origin: Origin::Safe,
                nil: NilVal::MayNil,
                err_var: ErrVar::Clean,
                writable: if sf & SLOT_INIT_WRITABLE != 0 {
                    WriteMode::Writable
                } else {
                    WriteMode::ReadOnly
                },
                err_link: ErrLink::None,
            });
        }
        Ok(Self {
            reachable: true,
            cells,
        })
    }

    fn copy_from(&mut self, other: &State) {
        self.reachable = other.reachable;
        self.cells.copy_from_slice(&other.cells);
    }

    fn join_from(&mut self, incoming: &State) -> bool {
        if !incoming.reachable {
            return false;
        }
        let mut changed = false;
        if !self.reachable {
            self.reachable = true;
            changed = true;
        }
        for (dst, &inc) in self.cells.iter_mut().zip(incoming.cells.iter()) {
            let joined = dst.join(inc);
            if joined != *dst {
                *dst = joined;
                changed = true;
            }
        }
        changed
    }

    fn relax_all_checked_nils(&mut self) {
        for c in &mut self.cells {
            c.nil = c.nil.relax_checked();
        }
    }

    fn promote_tuple(&mut self, tuple_id: u32) {
        for c in &mut self.cells {
            if c.origin == Origin::Single(tuple_id) {
                c.origin = Origin::Safe;
            }
        }
    }
}

/// Solves Phase 1 (interprocedural concrete-nil summary fixpoint) +
/// Phase 2 (intraprocedural product-lattice CFG dataflow fixpoint) across all functions.
pub fn solve_package<F>(funcs: &[FuncIR], check_cancel: &mut F) -> Result<PackageResult, SolveError>
where
    F: FnMut() -> bool,
{
    // Summaries only flip false -> true, so at most `funcs.len()` sweeps change one and one more
    // confirms: `funcs.len() + 1` is a proven bound and the cap below is an invariant check.
    solve_package_capped(funcs, check_cancel, funcs.len() + 1)
}

/// Union-find root with path halving.
fn uf_find(parent: &mut [u32], mut x: u32) -> u32 {
    while parent[x as usize] != x {
        let gp = parent[parent[x as usize] as usize];
        parent[x as usize] = gp;
        x = gp;
    }
    x
}

/// Largest set of tuples considered for merging at once. Each candidate bind block costs one
/// reachability sweep (a `Vec<bool>` over the blocks), so the bound keeps that work and memory
/// small; a larger class is left unmerged, which is the conservative (pre-merge) behavior.
const MAX_CANON_CLASS: usize = 64;

/// Maps every tuple to the canonical (smallest) id of its logical call result.
///
/// Lowering gives each call site its own tuple id and SSA gives each call's error its own slot.
/// When two call sites on mutually exclusive paths feed the same merged error through a phi
/// (`if c { v, err = f() } else { v, err = g() }`), the one check after the join establishes
/// `err == nil` for whichever call ran, so the two tuples must share an identity or `ErrLink`
/// and `Origin` degrade to "mixed" at the join and the check can no longer promote the
/// companion. Tuples merge only when their error slots are connected through phis, their flags
/// agree, and their bind blocks are pairwise unreachable from one another: two binds on one
/// path (`a, err := f(); if retry { _, err = g() }`) stay separate, because there the check
/// after the join does not prove `f`'s error nil. Binds inside a loop reach each other through
/// the back edge and so are never merged. Returns `None` when no tuple changes identity.
fn canonical_tuples<F: FnMut() -> bool>(
    fn_ir: &FuncIR,
    check_cancel: &mut F,
) -> Result<Option<Vec<u32>>, SolveError> {
    let n_tuples = fn_ir.tuples.len();
    if n_tuples < 2 || fn_ir.blocks.iter().all(|b| b.phis.is_empty()) {
        return Ok(None);
    }
    let mut parent: Vec<u32> = (0..fn_ir.slot_flags.len() as u32).collect();
    for block in &fn_ir.blocks {
        for phi in &block.phis {
            for &(_, src) in &phi.incoming {
                let a = uf_find(&mut parent, phi.dst_slot);
                let b = uf_find(&mut parent, src);
                if a != b {
                    parent[a.max(b) as usize] = a.min(b);
                }
            }
        }
    }
    let mut bind_blocks: Vec<Vec<u32>> = vec![Vec::new(); n_tuples];
    for (bb, block) in fn_ir.blocks.iter().enumerate() {
        for op in &block.ops {
            if let OpIR::BindTuple { tuple_id, .. } = op {
                let v = &mut bind_blocks[*tuple_id as usize];
                if v.last() != Some(&(bb as u32)) {
                    v.push(bb as u32);
                }
            }
        }
    }
    let mut classes: std::collections::BTreeMap<(u32, u8), Vec<u32>> =
        std::collections::BTreeMap::new();
    for (t, meta) in fn_ir.tuples.iter().enumerate() {
        if meta.err_slot != NONE_U32 && !bind_blocks[t].is_empty() {
            let key = (uf_find(&mut parent, meta.err_slot), meta.flags);
            classes.entry(key).or_default().push(t as u32);
        }
    }
    let mut canon: Vec<u32> = (0..n_tuples as u32).collect();
    let mut changed = false;
    for members in classes.values() {
        if members.len() < 2 || members.len() > MAX_CANON_CLASS {
            continue;
        }
        if !check_cancel() {
            return Err(SolveError::Cancelled);
        }
        let mut reach: std::collections::BTreeMap<u32, Vec<bool>> =
            std::collections::BTreeMap::new();
        for &t in members {
            for &bb in &bind_blocks[t as usize] {
                if let std::collections::btree_map::Entry::Vacant(e) = reach.entry(bb) {
                    if !check_cancel() {
                        return Err(SolveError::Cancelled);
                    }
                    e.insert(reachable_blocks_from(fn_ir, bb));
                }
            }
        }
        let exclusive = |t: u32, u: u32| {
            bind_blocks[t as usize].iter().all(|&bt| {
                bind_blocks[u as usize]
                    .iter()
                    .all(|&bu| bt != bu && !reach[&bt][bu as usize] && !reach[&bu][bt as usize])
            })
        };
        let mut groups: Vec<Vec<u32>> = Vec::new();
        for &t in members {
            match groups
                .iter_mut()
                .find(|g| g.iter().all(|&u| exclusive(t, u)))
            {
                Some(g) => {
                    canon[t as usize] = g[0];
                    changed = true;
                    g.push(t);
                }
                None => groups.push(vec![t]),
            }
        }
    }
    Ok(changed.then_some(canon))
}

/// Returns `fn_ir` with every `BindTuple` rewritten to its canonical tuple id, or `None` when
/// nothing changes.
fn with_canonical_tuples<F: FnMut() -> bool>(
    fn_ir: &FuncIR,
    check_cancel: &mut F,
) -> Result<Option<FuncIR>, SolveError> {
    let Some(canon) = canonical_tuples(fn_ir, check_cancel)? else {
        return Ok(None);
    };
    let mut out = fn_ir.clone();
    for block in &mut out.blocks {
        for op in &mut block.ops {
            if let OpIR::BindTuple { tuple_id, .. } = op {
                *tuple_id = canon[*tuple_id as usize];
            }
        }
    }
    Ok(Some(out))
}

/// Phase-1 counters, read by tests: the load-independent measure of phase-1 cost.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
struct Phase1Stats {
    /// `solve_function` calls made by phase 1.
    solves: u64,
    /// Work units of those solves (ops plus blocks x max(slots, 1) of each function solved, per solve).
    work: u64,
    /// Strongly connected components of the call graph that phase 1 visited.
    sccs: u64,
    /// Sweeps run, over all components, including each component's confirming sweep.
    sweeps: u64,
}

/// Polls `check_cancel` once per this many steps of the component search, which would
/// otherwise run unpolled over a package-sized component.
const SCC_POLL_STEPS: usize = 1024;

/// Strongly connected components of the graph whose edge `v -> w` means `w` calls `v`
/// (`callers[v]` lists the `w`), restricted to `active` nodes, found by an iterative Tarjan
/// search (a call chain of `MAX_FUNCS` functions would overflow the stack recursively).
///
/// Tarjan emits a component after every component reachable from it, and here that is every
/// component of its callers, so the emission order is callers first. The result is the reverse:
/// one flat list of the nodes in solve order (components callees first) and the exclusive end
/// offset of each component in it. Inside a component the nodes are in reverse finish order of
/// the depth-first search, which for the search over callers lists every function after the
/// functions it calls that were found through it, so a summary that flips travels to its callers
/// within one sweep wherever the search found them: the same callee-first order that is used
/// between components, applied inside the cycle. A cycle can still contain back edges against
/// that order; each costs one more sweep, and the work budget bounds the total.
fn call_sccs<F: FnMut() -> bool>(
    callers: &[Vec<u32>],
    active: &[bool],
    check_cancel: &mut F,
) -> Result<(Vec<u32>, Vec<u32>), SolveError> {
    const UNVISITED: u32 = u32::MAX;
    let n = callers.len();
    let mut index = vec![UNVISITED; n];
    let mut finish = vec![0u32; n];
    let mut next_finish = 0u32;
    let mut low = vec![0u32; n];
    let mut on_stack = vec![false; n];
    let mut stack: Vec<u32> = Vec::new();
    let mut frames: Vec<(u32, usize)> = Vec::new();
    let mut members: Vec<u32> = Vec::new();
    let mut ends: Vec<u32> = Vec::new();
    let mut next_index = 0u32;
    let mut steps = 0usize;
    for root in 0..n {
        if !active[root] || index[root] != UNVISITED {
            continue;
        }
        if !check_cancel() {
            return Err(SolveError::Cancelled);
        }
        index[root] = next_index;
        low[root] = next_index;
        next_index += 1;
        stack.push(root as u32);
        on_stack[root] = true;
        frames.push((root as u32, 0));
        while let Some(&(v, pos)) = frames.last() {
            steps += 1;
            if steps.is_multiple_of(SCC_POLL_STEPS) && !check_cancel() {
                return Err(SolveError::Cancelled);
            }
            let vi = v as usize;
            if let Some(&w) = callers[vi].get(pos) {
                if let Some(top) = frames.last_mut() {
                    top.1 += 1;
                }
                let wi = w as usize;
                if !active[wi] {
                    continue;
                }
                if index[wi] == UNVISITED {
                    index[wi] = next_index;
                    low[wi] = next_index;
                    next_index += 1;
                    stack.push(w);
                    on_stack[wi] = true;
                    frames.push((w, 0));
                } else if on_stack[wi] {
                    low[vi] = low[vi].min(index[wi]);
                }
                continue;
            }
            frames.pop();
            finish[vi] = next_finish;
            next_finish += 1;
            if low[vi] == index[vi] {
                while let Some(m) = stack.pop() {
                    on_stack[m as usize] = false;
                    members.push(m);
                    if m == v {
                        break;
                    }
                }
                ends.push(members.len() as u32);
            }
            if let Some(&(parent, _)) = frames.last() {
                let pi = parent as usize;
                low[pi] = low[pi].min(low[vi]);
            }
        }
    }
    // Callees first between components, reverse finish order inside one.
    let mut order: Vec<u32> = Vec::with_capacity(members.len());
    let mut order_ends: Vec<u32> = Vec::with_capacity(ends.len());
    for k in (0..ends.len()).rev() {
        let start = if k == 0 { 0 } else { ends[k - 1] as usize };
        let from = order.len();
        order.extend_from_slice(&members[start..ends[k] as usize]);
        order[from..].sort_unstable_by_key(|&m| Reverse(finish[m as usize]));
        order_ends.push(order.len() as u32);
    }
    Ok((order, order_ends))
}

/// `solve_package` with an explicit call-graph sweep cap (the public entry point passes
/// `funcs.len() + 1`, which bounds every component's sweeps; tests pass a small cap to reach
/// the error). The cap applies to each strongly connected component on its own.
pub fn solve_package_capped<F>(
    funcs: &[FuncIR],
    check_cancel: &mut F,
    max_callgraph_iters: usize,
) -> Result<PackageResult, SolveError>
where
    F: FnMut() -> bool,
{
    solve_package_limited(
        funcs,
        check_cancel,
        max_callgraph_iters,
        PHASE1_WORK_FACTOR,
        PHASE1_WORK_FLOOR,
        &mut Phase1Stats::default(),
    )
}

fn solve_package_limited<F>(
    funcs: &[FuncIR],
    check_cancel: &mut F,
    max_callgraph_iters: usize,
    work_factor: u64,
    work_floor: u64,
    stats: &mut Phase1Stats,
) -> Result<PackageResult, SolveError>
where
    F: FnMut() -> bool,
{
    let mut canon: Vec<Option<FuncIR>> = Vec::with_capacity(funcs.len());
    for f in funcs {
        canon.push(with_canonical_tuples(f, check_cancel)?);
    }
    let funcs_view: Vec<&FuncIR> = funcs
        .iter()
        .zip(canon.iter())
        .map(|(orig, c)| c.as_ref().unwrap_or(orig))
        .collect();
    let funcs = funcs_view.as_slice();
    let n_funcs = funcs.len();
    let mut may_return_nil = vec![false; n_funcs];

    // Seed external / bodyless functions that may return nil concrete error pointers.
    for (idx, fn_ir) in funcs.iter().copied().enumerate() {
        if (fn_ir.flags & FUNC_EXTERNAL_MAY_NIL != 0)
            || ((fn_ir.flags & FUNC_RETURNS_CONCRETE_ERR_PTR != 0) && fn_ir.blocks.is_empty())
        {
            may_return_nil[idx] = true;
        }
    }

    // Phase 1: Interprocedural call-graph fixpoint for concrete error pointer returns.
    // The summaries only flip false -> true, and a function's result depends on nothing but its
    // callees' summaries, so the least fixpoint is unique and any order that ends with every
    // function stable under the final summaries reaches it. Missing the sweep cap would export
    // optimistic (too-small) summaries that hide ERR004 findings, so it is an error.
    //
    // The order is callee-first: the call graph's strongly connected components are solved in
    // reverse topological order, so every callee outside a component is final before any of its
    // callers is first solved, and a function outside a cycle is solved exactly once. (In index
    // order a function calling every member of a caller-first chain was re-solved after each
    // member flipped: O(n^2), 186 s at 262,144 functions.) Inside a component the dirty-worklist
    // sweeps of the index-order phase are kept: `this_sweep` holds the dirty members still ahead
    // of the sweep's position and `next_sweep` those behind it, both min-heaps, so a sweep
    // visits them in index order and costs O((solves + call edges) x log functions). `dirty[i]`
    // is true while `i` is in a heap, when `i` can never be solved again (its summary is already
    // may-nil or it returns no concrete error pointer), and while `i` has not been reached yet
    // (a flipped callee's callers in later components are solved when their turn comes), so no
    // index is queued twice. Only functions that can still flip are graph nodes: an inactive
    // function is never solved, so an edge through it carries no information.
    //
    // A component that is itself a cycle can still cost O(n^2) (a hub calling every member of a
    // chain that closes back on the hub), so the solves are also bounded by a whole-package
    // work budget in a deterministic unit: the ops plus state cells (blocks x max(slots, 1))
    // of each function per solve, against `work_factor` x the package's total of that (at least `work_floor`). The unit
    // is the same in both engines, so both refuse the same packages at the same solve. Cap
    // and budget are checked after every solve, and `check_cancel` is polled per component and
    // per sweep.
    let mut callers: Vec<Vec<u32>> = vec![Vec::new(); n_funcs];
    let mut unit_work: Vec<u64> = Vec::with_capacity(n_funcs);
    for (idx, fn_ir) in funcs.iter().copied().enumerate() {
        // Solve cost scales with the state cells (blocks x slots) the solver holds, which is what
        // MAX_STATE_CELLS_PER_FUNC bounds, plus one step per op.
        let mut work = fn_ir.blocks.len() as u64 * (fn_ir.slot_flags.len() as u64).max(1);
        for block in &fn_ir.blocks {
            work += block.ops.len() as u64;
            for op in &block.ops {
                if let OpIR::CallConcretePtr { callee_idx, .. } = op {
                    let v = &mut callers[*callee_idx as usize];
                    if v.last() != Some(&(idx as u32)) {
                        v.push(idx as u32);
                    }
                }
            }
        }
        unit_work.push(work);
    }
    let size: u64 = unit_work.iter().sum();
    let budget = size.saturating_mul(work_factor).max(work_floor);
    let active: Vec<bool> = funcs
        .iter()
        .zip(&may_return_nil)
        .map(|(f, &nil)| !nil && f.flags & FUNC_RETURNS_CONCRETE_ERR_PTR != 0)
        .collect();
    let (order, scc_ends) = call_sccs(&callers, &active, check_cancel)?;
    // rank[f] is f's position in the solve order; the sweep heaps hold ranks, so a sweep visits
    // the dirty members of a component in that order and not in function-index order.
    let mut rank = vec![0u32; n_funcs];
    for (pos, &f) in order.iter().enumerate() {
        rank[f as usize] = pos as u32;
    }

    let mut dirty = vec![true; n_funcs];
    let mut this_sweep: BinaryHeap<Reverse<u32>> = BinaryHeap::new();
    let mut next_sweep: BinaryHeap<Reverse<u32>> = BinaryHeap::new();
    let mut work_done = 0u64;
    let mut start = 0u32;
    for &end in &scc_ends {
        stats.sccs += 1;
        this_sweep.extend((start..end).map(Reverse));
        start = end;
        let mut converged = false;
        for _iter in 0..max_callgraph_iters {
            if !check_cancel() {
                return Err(SolveError::Cancelled);
            }
            stats.sweeps += 1;
            let mut any_changed = false;
            while let Some(Reverse(pos)) = this_sweep.pop() {
                let idx32 = order[pos as usize];
                let idx = idx32 as usize;
                let fn_ir = funcs[idx];
                if may_return_nil[idx] || !dirty[idx] {
                    continue;
                }
                if !check_cancel() {
                    return Err(SolveError::Cancelled);
                }
                dirty[idx] = false;
                let solved = solve_function(idx32, fn_ir, &may_return_nil, false, check_cancel)?;
                stats.solves += 1;
                work_done = work_done.saturating_add(unit_work[idx]);
                stats.work = work_done;
                if work_done > budget {
                    return Err(SolveError::WorkBudgetExceeded(work_done, budget));
                }
                if solved.func_may_return_nil {
                    may_return_nil[idx] = true;
                    any_changed = true;
                    for &c in &callers[idx] {
                        if dirty[c as usize] {
                            continue;
                        }
                        dirty[c as usize] = true;
                        let r = rank[c as usize];
                        if r > pos {
                            this_sweep.push(Reverse(r));
                        } else {
                            next_sweep.push(Reverse(r));
                        }
                    }
                }
            }
            if !any_changed {
                converged = true;
                break;
            }
            std::mem::swap(&mut this_sweep, &mut next_sweep);
        }
        if !converged {
            return Err(SolveError::CallgraphBudgetExceeded(max_callgraph_iters));
        }
    }

    // Phase 2: Full diagnostic solve with converged interprocedural summaries.
    let mut findings = Vec::new();
    for (idx, fn_ir) in funcs.iter().copied().enumerate() {
        if !check_cancel() {
            return Err(SolveError::Cancelled);
        }
        if fn_ir.blocks.is_empty() {
            continue;
        }
        let solved = solve_function(idx as u32, fn_ir, &may_return_nil, true, check_cancel)?;
        findings.extend(solved.findings);
    }

    findings.sort_unstable();
    findings.dedup();

    Ok(PackageResult {
        may_return_nil,
        findings,
    })
}

struct FuncSolved {
    func_may_return_nil: bool,
    findings: Vec<Finding>,
}

fn apply_phis_into(
    dst: &mut State,
    block: &BlockIR,
    base: &State,
    out_edge_states: &[Vec<(u32, State)>],
    bb: u32,
) {
    dst.copy_from(base);
    if !dst.reachable || block.phis.is_empty() {
        return;
    }
    for phi in &block.phis {
        let mut joined = SlotCell::BOT;
        for &(pred_bb, src_slot) in &phi.incoming {
            let s = src_slot as usize;
            if let Some(edges) = out_edge_states.get(pred_bb as usize) {
                for (target, edge_st) in edges {
                    if *target == bb && edge_st.reachable {
                        joined = joined.join(edge_st.cells[s]);
                    }
                }
            }
        }
        let d = &mut dst.cells[phi.dst_slot as usize];
        if joined.origin != Origin::Bot {
            d.origin = joined.origin;
        }
        if joined.nil != NilVal::Bot {
            d.nil = joined.nil;
        }
        if joined.err_var != ErrVar::Bot {
            d.err_var = joined.err_var;
        }
        if joined.writable != WriteMode::Bot {
            d.writable = joined.writable;
        }
        if joined.err_link != ErrLink::Bot {
            d.err_link = joined.err_link;
        }
    }
}

struct DiagSink<'a> {
    func_idx: u32,
    bb: u32,
    findings: &'a mut Vec<Finding>,
    deferred_closes: &'a mut Vec<(u32, u32, u32, bool)>,
}

fn step_op(
    op: &OpIR,
    st: &mut State,
    fn_ir: &FuncIR,
    may_return_nil: &[bool],
    sink: Option<&mut DiagSink<'_>>,
) {
    match op {
        OpIR::UnhandledErr { site_id } => {
            if let Some(s) = sink {
                s.findings.push(Finding {
                    func_idx: s.func_idx,
                    site_id: *site_id,
                    rule_code: RULE_ERR001,
                    sub_kind: SUB_DEFAULT,
                });
            }
        }
        OpIR::BindTuple {
            site_id,
            tuple_id,
            err_slot,
            companions,
        } => {
            // Attempting a new error-returning operation acts as fallback/recovery for prior checks.
            st.relax_all_checked_nils();

            let tmeta = &fn_ir.tuples[*tuple_id as usize];
            let blanked = (tmeta.flags & TUPLE_ERR_BLANKED) != 0 || *err_slot == NONE_U32;
            let comp_origin = if blanked {
                Origin::Blanked(*tuple_id)
            } else {
                Origin::Single(*tuple_id)
            };

            if *err_slot != NONE_U32 {
                let ec = &mut st.cells[*err_slot as usize];
                if matches!(ec.err_var, ErrVar::Unread(_)) {
                    if let Some(s) = sink {
                        s.findings.push(Finding {
                            func_idx: s.func_idx,
                            site_id: *site_id,
                            rule_code: RULE_ERR003,
                            sub_kind: SUB_ERR003_OVERWRITTEN,
                        });
                    }
                }
                ec.err_var = ErrVar::Unread(*site_id);
                ec.nil = NilVal::MayNil;
                ec.err_link = ErrLink::Tuple(*tuple_id);
                ec.origin = Origin::Safe;
            }

            for &cs in companions {
                let cc = &mut st.cells[cs as usize];
                cc.origin = comp_origin;
                cc.nil = NilVal::MayNil;
                cc.err_var = ErrVar::Clean;
                cc.err_link = ErrLink::None;
            }
        }
        OpIR::BindErr { site_id, err_slot } => {
            st.relax_all_checked_nils();
            let ec = &mut st.cells[*err_slot as usize];
            if matches!(ec.err_var, ErrVar::Unread(_)) {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR003,
                        sub_kind: SUB_ERR003_OVERWRITTEN,
                    });
                }
            }
            ec.err_var = ErrVar::Unread(*site_id);
            ec.nil = NilVal::MayNil;
            ec.err_link = ErrLink::None;
            ec.origin = Origin::Safe;
        }
        OpIR::AssignSlot {
            site_id,
            dst_slot,
            src_slot,
            nil_hint,
            writable_hint,
        } => {
            let d = *dst_slot as usize;
            if *src_slot == NONE_U32 {
                // Overwriting an unread error variable with a constant (e.g., err = nil) loses it.
                let dc = &mut st.cells[d];
                if matches!(dc.err_var, ErrVar::Unread(_)) {
                    if let Some(s) = sink {
                        s.findings.push(Finding {
                            func_idx: s.func_idx,
                            site_id: *site_id,
                            rule_code: RULE_ERR003,
                            sub_kind: SUB_ERR003_OVERWRITTEN,
                        });
                    }
                }
                dc.origin = Origin::Safe;
                dc.err_var = ErrVar::Clean;
                dc.err_link = ErrLink::None;
                dc.nil = match *nil_hint {
                    ASSIGN_NIL_NON_NIL => NilVal::NonNil,
                    ASSIGN_NIL_MUST_NIL => NilVal::MustNil,
                    _ => NilVal::MayNil,
                };
                dc.writable = if *writable_hint == 1 {
                    WriteMode::Writable
                } else {
                    WriteMode::ReadOnly
                };
            } else {
                let s_idx = *src_slot as usize;
                let src_cell = st.cells[s_idx];

                if d != s_idx && matches!(st.cells[d].err_var, ErrVar::Unread(_)) {
                    if let Some(s) = sink {
                        s.findings.push(Finding {
                            func_idx: s.func_idx,
                            site_id: *site_id,
                            rule_code: RULE_ERR003,
                            sub_kind: SUB_ERR003_OVERWRITTEN,
                        });
                    }
                }
                if d != s_idx {
                    st.cells[s_idx].err_var = ErrVar::Clean;
                }
                let dc = &mut st.cells[d];
                dc.origin = src_cell.origin;
                dc.nil = match *nil_hint {
                    ASSIGN_NIL_COPY => src_cell.nil,
                    ASSIGN_NIL_NON_NIL => NilVal::NonNil,
                    ASSIGN_NIL_MUST_NIL => NilVal::MustNil,
                    _ => NilVal::MayNil,
                };
                dc.err_var = src_cell.err_var;
                dc.writable = if *writable_hint == 1 {
                    WriteMode::Writable
                } else {
                    src_cell.writable
                };
                dc.err_link = src_cell.err_link;
            }
        }
        OpIR::CallConcretePtr {
            dst_slot,
            callee_idx,
            ..
        } => {
            let dc = &mut st.cells[*dst_slot as usize];
            dc.origin = Origin::Safe;
            dc.err_var = ErrVar::Clean;
            dc.err_link = ErrLink::None;
            dc.writable = WriteMode::ReadOnly;
            dc.nil = if may_return_nil[*callee_idx as usize] {
                NilVal::MayNil
            } else {
                NilVal::NonNil
            };
        }
        OpIR::ObserveErr { slot, .. } => {
            let c = &mut st.cells[*slot as usize];
            c.err_var = ErrVar::Clean;
            c.nil = c.nil.relax_checked();
        }
        OpIR::UseCompanion { site_id, slot } => match st.cells[*slot as usize].origin {
            Origin::Blanked(_) => {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR002,
                        sub_kind: SUB_ERR002_BLANKED,
                    });
                }
            }
            Origin::Single(_) | Origin::UncheckedMix(_) => {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR002,
                        sub_kind: SUB_ERR002_UNCHECKED,
                    });
                }
            }
            Origin::Bot | Origin::Safe => {}
        },
        OpIR::ShadowErr {
            site_id,
            inner_slot,
            ..
        } => {
            let _ = *inner_slot;
            if let Some(s) = sink {
                s.findings.push(Finding {
                    func_idx: s.func_idx,
                    site_id: *site_id,
                    rule_code: RULE_ERR003,
                    sub_kind: SUB_ERR003_SHADOWED,
                });
            }
        }
        OpIR::BoxConcreteErr { site_id, ptr_slot } => match st.cells[*ptr_slot as usize].nil {
            NilVal::MustNil => {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR004,
                        sub_kind: SUB_ERR004_MUST_NIL,
                    });
                }
            }
            NilVal::MayNil => {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR004,
                        sub_kind: SUB_ERR004_MAY_NIL,
                    });
                }
            }
            NilVal::Bot | NilVal::NonNil | NilVal::NonNilChecked => {}
        },
        OpIR::MarkWritable { res_slot, .. } => {
            st.cells[*res_slot as usize].writable = WriteMode::Writable;
        }
        OpIR::UnhandledClose {
            site_id,
            res_slot,
            is_defer,
        } => {
            let now_writable = st.cells[*res_slot as usize].writable == WriteMode::Writable;
            if *is_defer {
                if let Some(s) = sink {
                    s.deferred_closes
                        .push((s.bb, *site_id, *res_slot, now_writable));
                }
            } else if now_writable {
                if let Some(s) = sink {
                    s.findings.push(Finding {
                        func_idx: s.func_idx,
                        site_id: *site_id,
                        rule_code: RULE_ERR005,
                        sub_kind: SUB_ERR005_SYNC_CLOSE,
                    });
                }
            }
        }
    }
}

fn write_outgoing_edges(
    edges_slot: &mut [(u32, State)],
    block: &BlockIR,
    end_state: &State,
    fn_ir: &FuncIR,
) {
    if !end_state.reachable {
        for (_, est) in edges_slot.iter_mut() {
            est.reachable = false;
        }
        return;
    }
    match block.term {
        Terminator::Jump { target } => {
            edges_slot[0].0 = target;
            edges_slot[0].1.copy_from(end_state);
        }
        Terminator::Cond {
            true_bb,
            false_bb,
            cond_kind,
            cond_slot,
        } => {
            edges_slot[0].0 = true_bb;
            edges_slot[0].1.copy_from(end_state);
            edges_slot[1].0 = false_bb;
            edges_slot[1].1.copy_from(end_state);
            let (first, rest) = edges_slot.split_at_mut(1);
            let t_state = &mut first[0].1;
            let f_state = &mut rest[0].1;

            match cond_kind {
                COND_IS_NIL => {
                    let cs = cond_slot as usize;
                    t_state.cells[cs].err_var = ErrVar::Clean;
                    f_state.cells[cs].err_var = ErrVar::Clean;

                    t_state.cells[cs].nil = NilVal::MustNil;
                    if let ErrLink::Tuple(tid) = end_state.cells[cs].err_link {
                        t_state.promote_tuple(tid);
                    }

                    f_state.cells[cs].nil = if (fn_ir.slot_flags[cs] & SLOT_IS_ERROR) != 0 {
                        NilVal::NonNilChecked
                    } else {
                        NilVal::NonNil
                    };
                }
                COND_NOT_NIL => {
                    let cs = cond_slot as usize;
                    t_state.cells[cs].err_var = ErrVar::Clean;
                    f_state.cells[cs].err_var = ErrVar::Clean;

                    t_state.cells[cs].nil = if (fn_ir.slot_flags[cs] & SLOT_IS_ERROR) != 0 {
                        NilVal::NonNilChecked
                    } else {
                        NilVal::NonNil
                    };

                    f_state.cells[cs].nil = NilVal::MustNil;
                    if let ErrLink::Tuple(tid) = end_state.cells[cs].err_link {
                        f_state.promote_tuple(tid);
                    }
                }
                COND_GT_ZERO => {
                    let cs = cond_slot as usize;
                    if let Origin::Single(tid) = t_state.cells[cs].origin {
                        if (fn_ir.tuples[tid as usize].flags & TUPLE_IO_READ) != 0 {
                            t_state.cells[cs].origin = Origin::Safe;
                        }
                    }
                }
                _ => {}
            }
        }
        Terminator::Return { .. } | Terminator::Stop => {}
    }
}

fn reachable_blocks_from(fn_ir: &FuncIR, start_bb: u32) -> Vec<bool> {
    let n = fn_ir.blocks.len();
    let mut seen = vec![false; n];
    let mut q = VecDeque::new();
    seen[start_bb as usize] = true;
    q.push_back(start_bb);
    while let Some(bb) = q.pop_front() {
        match fn_ir.blocks[bb as usize].term {
            Terminator::Jump { target } => {
                if !seen[target as usize] {
                    seen[target as usize] = true;
                    q.push_back(target);
                }
            }
            Terminator::Cond {
                true_bb, false_bb, ..
            } => {
                if !seen[true_bb as usize] {
                    seen[true_bb as usize] = true;
                    q.push_back(true_bb);
                }
                if !seen[false_bb as usize] {
                    seen[false_bb as usize] = true;
                    q.push_back(false_bb);
                }
            }
            Terminator::Return { .. } | Terminator::Stop => {}
        }
    }
    seen
}

fn solve_function<F>(
    func_idx: u32,
    fn_ir: &FuncIR,
    may_return_nil: &[bool],
    collect_findings: bool,
    check_cancel: &mut F,
) -> Result<FuncSolved, SolveError>
where
    F: FnMut() -> bool,
{
    let n_blocks = fn_ir.blocks.len();
    let n_slots = fn_ir.slot_flags.len();

    // Every state vector below is allocated fallibly: with the per-function budget in
    // `decode_package` this is at most `MAX_STATE_CELLS_PER_FUNC * 112` bytes, and a refusal
    // from the allocator fails the package instead of aborting the process.
    let mut in_states: Vec<State> = try_vec(n_blocks, "block entry states")?;
    let mut end_states: Vec<State> = try_vec(n_blocks, "block exit states")?;
    let mut out_edges: Vec<Vec<(u32, State)>> = try_vec(n_blocks, "edge states")?;
    for b in &fn_ir.blocks {
        in_states.push(State::try_bottom(n_slots)?);
        end_states.push(State::try_bottom(n_slots)?);
        let mut edges: Vec<(u32, State)> = try_vec(2, "edge states")?;
        match b.term {
            Terminator::Jump { target } => edges.push((target, State::try_bottom(n_slots)?)),
            Terminator::Cond {
                true_bb, false_bb, ..
            } => {
                edges.push((true_bb, State::try_bottom(n_slots)?));
                edges.push((false_bb, State::try_bottom(n_slots)?));
            }
            Terminator::Return { .. } | Terminator::Stop => {}
        }
        out_edges.push(edges);
    }

    in_states[0] = State::try_entry(&fn_ir.slot_flags)?;
    let mut scratch = State::try_bottom(n_slots)?;
    let mut edge_scratch = [
        (0u32, State::try_bottom(n_slots)?),
        (0u32, State::try_bottom(n_slots)?),
    ];

    let mut in_queue = vec![false; n_blocks];
    let mut worklist: VecDeque<u32> = VecDeque::new();
    worklist
        .try_reserve_exact(n_blocks)
        .map_err(|_| SolveError::AllocationFailed("worklist"))?;
    worklist.push_back(0u32);
    in_queue[0] = true;

    let mut steps: usize = 0;
    while let Some(bb) = worklist.pop_front() {
        let bb_usize = bb as usize;
        in_queue[bb_usize] = false;
        steps += 1;
        if steps > MAX_ITERATIONS_PER_FUNC {
            return Err(SolveError::IterationBudgetExceeded(func_idx));
        }
        if (steps & 63) == 0 && !check_cancel() {
            return Err(SolveError::Cancelled);
        }

        let block = &fn_ir.blocks[bb_usize];
        apply_phis_into(&mut scratch, block, &in_states[bb_usize], &out_edges, bb);
        if !scratch.reachable {
            continue;
        }
        for op in &block.ops {
            step_op(op, &mut scratch, fn_ir, may_return_nil, None);
        }
        let arity = out_edges[bb_usize].len();
        write_outgoing_edges(&mut edge_scratch[..arity], block, &scratch, fn_ir);
        end_states[bb_usize].copy_from(&scratch);

        for k in 0..arity {
            let succ = edge_scratch[k].0;
            out_edges[bb_usize][k].0 = succ;
            let edge_changed = out_edges[bb_usize][k].1.join_from(&edge_scratch[k].1);
            let in_changed = in_states[succ as usize].join_from(&out_edges[bb_usize][k].1);
            if (edge_changed || in_changed) && !in_queue[succ as usize] {
                in_queue[succ as usize] = true;
                worklist.push_back(succ);
            }
        }
    }

    // Determine whether this function may return a nil concrete error pointer.
    let mut func_may_return_nil = false;
    if (fn_ir.flags & FUNC_RETURNS_CONCRETE_ERR_PTR) != 0 {
        for (bb_idx, block) in fn_ir.blocks.iter().enumerate() {
            let st = &end_states[bb_idx];
            if !st.reachable {
                continue;
            }
            if let Terminator::Return {
                ret_slot, flags, ..
            } = block.term
            {
                if (flags & RET_CONCRETE_LITERAL_NIL) != 0 {
                    func_may_return_nil = true;
                    break;
                }
                if ret_slot != NONE_U32 {
                    match st.cells[ret_slot as usize].nil {
                        NilVal::MustNil | NilVal::MayNil => {
                            func_may_return_nil = true;
                            break;
                        }
                        NilVal::Bot | NilVal::NonNil | NilVal::NonNilChecked => {}
                    }
                }
            }
        }
    }

    if !collect_findings {
        return Ok(FuncSolved {
            func_may_return_nil,
            findings: Vec::new(),
        });
    }

    // Single diagnostic extraction pass over converged block states.
    let mut findings = Vec::new();
    let mut deferred_closes: Vec<(u32, u32, u32, bool)> = Vec::new();

    for (bb_idx, block) in fn_ir.blocks.iter().enumerate() {
        let bb = bb_idx as u32;
        if !in_states[bb_idx].reachable {
            continue;
        }
        apply_phis_into(&mut scratch, block, &in_states[bb_idx], &out_edges, bb);
        let mut sink = DiagSink {
            func_idx,
            bb,
            findings: &mut findings,
            deferred_closes: &mut deferred_closes,
        };
        for op in &block.ops {
            step_op(op, &mut scratch, fn_ir, may_return_nil, Some(&mut sink));
        }

        // Check Return-level nilerr (`if err != nil { return nil }`).
        if let Terminator::Return {
            site_id,
            ret_slot,
            flags,
        } = block.term
        {
            if (fn_ir.flags & FUNC_RETURNS_ERROR) != 0 && (flags & RET_LITERAL_NIL_ERR) != 0 {
                let mut active_checked_non_nil = false;
                for (s_idx, cell) in scratch.cells.iter().enumerate() {
                    if cell.nil == NilVal::NonNilChecked
                        && (fn_ir.slot_flags[s_idx] & SLOT_IS_ERROR) != 0
                    {
                        active_checked_non_nil = true;
                        break;
                    }
                }
                let _ = ret_slot;
                if active_checked_non_nil {
                    findings.push(Finding {
                        func_idx,
                        site_id,
                        rule_code: RULE_ERR003,
                        sub_kind: SUB_ERR003_NILERR,
                    });
                }
            }
        }
    }

    // Evaluate deferred Close() calls against reachable Return blocks (and registration state).
    for (defer_bb, site_id, res_slot, writable_at_reg) in deferred_closes {
        let mut writable_on_exit = writable_at_reg;
        if !writable_on_exit {
            let reach = reachable_blocks_from(fn_ir, defer_bb);
            for (bb_idx, &can_reach) in reach.iter().enumerate() {
                if !can_reach || !end_states[bb_idx].reachable {
                    continue;
                }
                if matches!(fn_ir.blocks[bb_idx].term, Terminator::Return { .. })
                    && end_states[bb_idx].cells[res_slot as usize].writable == WriteMode::Writable
                {
                    writable_on_exit = true;
                    break;
                }
            }
        }
        if writable_on_exit {
            findings.push(Finding {
                func_idx,
                site_id,
                rule_code: RULE_ERR005,
                sub_kind: SUB_ERR005_DEFER_CLOSE,
            });
        }
    }

    Ok(FuncSolved {
        func_may_return_nil,
        findings,
    })
}

/// Encodes `FuncIR` list into a `GSE1` binary request frame (used by Rust tests and fuzzing).
pub fn encode_package(funcs: &[FuncIR]) -> Vec<u8> {
    let mut out = Vec::new();
    out.extend_from_slice(&REQ_MAGIC);
    out.extend_from_slice(&(funcs.len() as u32).to_le_bytes());
    for fn_ir in funcs {
        out.push(fn_ir.flags);
        out.extend_from_slice(&(fn_ir.slot_flags.len() as u32).to_le_bytes());
        out.extend_from_slice(&(fn_ir.tuples.len() as u32).to_le_bytes());
        out.extend_from_slice(&(fn_ir.blocks.len() as u32).to_le_bytes());
        out.extend_from_slice(&fn_ir.slot_flags);
        for t in &fn_ir.tuples {
            out.extend_from_slice(&t.err_slot.to_le_bytes());
            out.extend_from_slice(&t.call_site_id.to_le_bytes());
            out.push(t.flags);
        }
        for b in &fn_ir.blocks {
            match b.term {
                Terminator::Jump { target } => {
                    out.push(TERM_JUMP);
                    out.extend_from_slice(&target.to_le_bytes());
                }
                Terminator::Cond {
                    true_bb,
                    false_bb,
                    cond_kind,
                    cond_slot,
                } => {
                    out.push(TERM_COND);
                    out.extend_from_slice(&true_bb.to_le_bytes());
                    out.extend_from_slice(&false_bb.to_le_bytes());
                    out.push(cond_kind);
                    out.extend_from_slice(&cond_slot.to_le_bytes());
                }
                Terminator::Return {
                    site_id,
                    ret_slot,
                    flags,
                } => {
                    out.push(TERM_RETURN);
                    out.extend_from_slice(&site_id.to_le_bytes());
                    out.extend_from_slice(&ret_slot.to_le_bytes());
                    out.push(flags);
                }
                Terminator::Stop => {
                    out.push(TERM_STOP);
                }
            }
            out.extend_from_slice(&(b.phis.len() as u32).to_le_bytes());
            for phi in &b.phis {
                out.extend_from_slice(&phi.dst_slot.to_le_bytes());
                out.extend_from_slice(&(phi.incoming.len() as u16).to_le_bytes());
                for &(pred, src) in &phi.incoming {
                    out.extend_from_slice(&pred.to_le_bytes());
                    out.extend_from_slice(&src.to_le_bytes());
                }
            }
            out.extend_from_slice(&(b.ops.len() as u32).to_le_bytes());
            for op in &b.ops {
                match op {
                    OpIR::UnhandledErr { site_id } => {
                        out.push(OP_UNHANDLED_ERR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                    }
                    OpIR::BindTuple {
                        site_id,
                        tuple_id,
                        err_slot,
                        companions,
                    } => {
                        out.push(OP_BIND_TUPLE);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&tuple_id.to_le_bytes());
                        out.extend_from_slice(&err_slot.to_le_bytes());
                        out.extend_from_slice(&(companions.len() as u16).to_le_bytes());
                        for &cs in companions {
                            out.extend_from_slice(&cs.to_le_bytes());
                        }
                    }
                    OpIR::BindErr { site_id, err_slot } => {
                        out.push(OP_BIND_ERR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&err_slot.to_le_bytes());
                    }
                    OpIR::AssignSlot {
                        site_id,
                        dst_slot,
                        src_slot,
                        nil_hint,
                        writable_hint,
                    } => {
                        out.push(OP_ASSIGN_SLOT);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&dst_slot.to_le_bytes());
                        out.extend_from_slice(&src_slot.to_le_bytes());
                        out.push(*nil_hint);
                        out.push(*writable_hint);
                    }
                    OpIR::CallConcretePtr {
                        site_id,
                        dst_slot,
                        callee_idx,
                    } => {
                        out.push(OP_CALL_CONCRETE_PTR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&dst_slot.to_le_bytes());
                        out.extend_from_slice(&callee_idx.to_le_bytes());
                    }
                    OpIR::ObserveErr { site_id, slot } => {
                        out.push(OP_OBSERVE_ERR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&slot.to_le_bytes());
                    }
                    OpIR::UseCompanion { site_id, slot } => {
                        out.push(OP_USE_COMPANION);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&slot.to_le_bytes());
                    }
                    OpIR::ShadowErr {
                        site_id,
                        inner_slot,
                        outer_slot,
                    } => {
                        out.push(OP_SHADOW_ERR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&inner_slot.to_le_bytes());
                        out.extend_from_slice(&outer_slot.to_le_bytes());
                    }
                    OpIR::BoxConcreteErr { site_id, ptr_slot } => {
                        out.push(OP_BOX_CONCRETE_ERR);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&ptr_slot.to_le_bytes());
                    }
                    OpIR::MarkWritable { site_id, res_slot } => {
                        out.push(OP_MARK_WRITABLE);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&res_slot.to_le_bytes());
                    }
                    OpIR::UnhandledClose {
                        site_id,
                        res_slot,
                        is_defer,
                    } => {
                        out.push(OP_UNHANDLED_CLOSE);
                        out.extend_from_slice(&site_id.to_le_bytes());
                        out.extend_from_slice(&res_slot.to_le_bytes());
                        out.push(u8::from(*is_defer));
                    }
                }
            }
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rejects_malformed_frames() {
        assert_eq!(solve_frame(b"", || true), Err(SolveError::Truncated));
        assert_eq!(
            solve_frame(b"NOPE\x00\x00\x00\x00", || true),
            Err(SolveError::BadMagic)
        );
        assert_eq!(
            solve_frame(b"GSE1\x00\x00\x00\x00extra", || true),
            Err(SolveError::TrailingBytes(5))
        );
        // Oversized num_funcs.
        let mut huge = Vec::from(&b"GSE1"[..]);
        huge.extend_from_slice(&(MAX_FUNCS as u32 + 1).to_le_bytes());
        assert_eq!(
            solve_frame(&huge, || true),
            Err(SolveError::LimitExceeded("num_funcs"))
        );
    }

    #[test]
    fn empty_package_roundtrip() {
        let frame = encode_package(&[]);
        let out = solve_frame(&frame, || true).expect("solve empty");
        assert_eq!(&out[0..4], b"GSR1");
        assert_eq!(u32::from_le_bytes(out[4..8].try_into().unwrap()), 0);
        assert_eq!(u32::from_le_bytes(out[8..12].try_into().unwrap()), 0);
    }

    #[test]
    fn cancellation_propagates() {
        let fn_ir = FuncIR {
            flags: 0,
            slot_flags: vec![0],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops: vec![OpIR::UnhandledErr { site_id: 1 }],
                term: Terminator::Return {
                    site_id: 2,
                    ret_slot: NONE_U32,
                    flags: 0,
                },
            }],
        };
        let frame = encode_package(&[fn_ir]);
        assert_eq!(solve_frame(&frame, || false), Err(SolveError::Cancelled));
    }

    #[test]
    fn err002_fallthrough_vs_guarded_and_fallback() {
        // Case A: if err != nil { log() }; use(fi) -> ERR002!
        let buggy = FuncIR {
            flags: 0,
            slot_flags: vec![0, SLOT_IS_ERROR], // 0: fi, 1: err
            tuples: vec![TupleMeta {
                err_slot: 1,
                call_site_id: 10,
                flags: 0,
            }],
            blocks: vec![
                // bb0: fi, err := os.Stat(path); if err != nil -> bb1 else bb2
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindTuple {
                        site_id: 10,
                        tuple_id: 0,
                        err_slot: 1,
                        companions: vec![0],
                    }],
                    term: Terminator::Cond {
                        true_bb: 1,
                        false_bb: 2,
                        cond_kind: COND_NOT_NIL,
                        cond_slot: 1,
                    },
                },
                // bb1: println("failed"); jump bb2 (forgot return!)
                BlockIR {
                    phis: vec![],
                    ops: vec![],
                    term: Terminator::Jump { target: 2 },
                },
                // bb2: fi.Size() -> should report ERR002 at site 20!
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::UseCompanion {
                        site_id: 20,
                        slot: 0,
                    }],
                    term: Terminator::Return {
                        site_id: 21,
                        ret_slot: NONE_U32,
                        flags: 0,
                    },
                },
            ],
        };

        // Case B: if err != nil { fi = fallback }; use(fi) -> CLEAN!
        let fallback_ok = FuncIR {
            flags: 0,
            slot_flags: vec![0, SLOT_IS_ERROR],
            tuples: vec![TupleMeta {
                err_slot: 1,
                call_site_id: 30,
                flags: 0,
            }],
            blocks: vec![
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindTuple {
                        site_id: 30,
                        tuple_id: 0,
                        err_slot: 1,
                        companions: vec![0],
                    }],
                    term: Terminator::Cond {
                        true_bb: 1,
                        false_bb: 2,
                        cond_kind: COND_NOT_NIL,
                        cond_slot: 1,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::AssignSlot {
                        site_id: 31,
                        dst_slot: 0,
                        src_slot: NONE_U32,
                        nil_hint: ASSIGN_NIL_NON_NIL,
                        writable_hint: 0,
                    }],
                    term: Terminator::Jump { target: 2 },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::UseCompanion {
                        site_id: 32,
                        slot: 0,
                    }],
                    term: Terminator::Return {
                        site_id: 33,
                        ret_slot: NONE_U32,
                        flags: 0,
                    },
                },
            ],
        };

        let res = solve_package(&[buggy, fallback_ok], &mut || true).unwrap();
        assert_eq!(
            res.findings,
            vec![Finding {
                func_idx: 0,
                site_id: 20,
                rule_code: RULE_ERR002,
                sub_kind: SUB_ERR002_UNCHECKED,
            }]
        );
    }

    #[test]
    fn err003_nilerr_vs_fallback_recovery() {
        // Func 0: if err != nil { return nil } -> ERR003 (nilerr).
        let nilerr_fn = FuncIR {
            flags: FUNC_RETURNS_ERROR,
            slot_flags: vec![SLOT_IS_ERROR],
            tuples: vec![],
            blocks: vec![
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindErr {
                        site_id: 1,
                        err_slot: 0,
                    }],
                    term: Terminator::Cond {
                        true_bb: 1,
                        false_bb: 2,
                        cond_kind: COND_NOT_NIL,
                        cond_slot: 0,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![],
                    term: Terminator::Return {
                        site_id: 2,
                        ret_slot: NONE_U32,
                        flags: RET_LITERAL_NIL_ERR,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![],
                    term: Terminator::Return {
                        site_id: 3,
                        ret_slot: NONE_U32,
                        flags: RET_LITERAL_NIL_ERR,
                    },
                },
            ],
        };

        // Func 1: if err1 != nil { err2 := retry(); if err2 == nil { return nil }; return err2 } -> CLEAN!
        let recovery_fn = FuncIR {
            flags: FUNC_RETURNS_ERROR,
            slot_flags: vec![SLOT_IS_ERROR, SLOT_IS_ERROR],
            tuples: vec![],
            blocks: vec![
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindErr {
                        site_id: 10,
                        err_slot: 0,
                    }],
                    term: Terminator::Cond {
                        true_bb: 1,
                        false_bb: 3,
                        cond_kind: COND_NOT_NIL,
                        cond_slot: 0,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindErr {
                        site_id: 11,
                        err_slot: 1,
                    }],
                    term: Terminator::Cond {
                        true_bb: 2,
                        false_bb: 3,
                        cond_kind: COND_IS_NIL,
                        cond_slot: 1,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![],
                    term: Terminator::Return {
                        site_id: 12,
                        ret_slot: NONE_U32,
                        flags: RET_LITERAL_NIL_ERR,
                    },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::ObserveErr {
                        site_id: 13,
                        slot: 1,
                    }],
                    term: Terminator::Return {
                        site_id: 14,
                        ret_slot: 1,
                        flags: 0,
                    },
                },
            ],
        };

        let res = solve_package(&[nilerr_fn, recovery_fn], &mut || true).unwrap();
        assert_eq!(
            res.findings,
            vec![Finding {
                func_idx: 0,
                site_id: 2,
                rule_code: RULE_ERR003,
                sub_kind: SUB_ERR003_NILERR,
            }]
        );
    }

    #[test]
    fn err004_interprocedural_concrete_nil_chain() {
        // f0 (leaf): returns literal nil *MyErr -> may_return_nil = true
        let f0 = FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![SLOT_IS_ERROR],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops: vec![],
                term: Terminator::Return {
                    site_id: 1,
                    ret_slot: NONE_U32,
                    flags: RET_CONCRETE_LITERAL_NIL,
                },
            }],
        };
        // f1 (mid): returns f0() -> inherits may_return_nil = true!
        let f1 = FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![SLOT_IS_ERROR],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops: vec![OpIR::CallConcretePtr {
                    site_id: 2,
                    dst_slot: 0,
                    callee_idx: 0,
                }],
                term: Terminator::Return {
                    site_id: 3,
                    ret_slot: 0,
                    flags: 0,
                },
            }],
        };
        // f2 (entry): boxes f1() into error without != nil guard -> ERR004!
        let f2 = FuncIR {
            flags: FUNC_RETURNS_ERROR,
            slot_flags: vec![SLOT_IS_ERROR],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops: vec![
                    OpIR::CallConcretePtr {
                        site_id: 4,
                        dst_slot: 0,
                        callee_idx: 1,
                    },
                    OpIR::BoxConcreteErr {
                        site_id: 5,
                        ptr_slot: 0,
                    },
                ],
                term: Terminator::Return {
                    site_id: 6,
                    ret_slot: 0,
                    flags: 0,
                },
            }],
        };

        let res = solve_package(&[f0, f1, f2], &mut || true).unwrap();
        assert_eq!(res.may_return_nil, vec![true, true, false]);
        assert_eq!(
            res.findings,
            vec![Finding {
                func_idx: 2,
                site_id: 5,
                rule_code: RULE_ERR004,
                sub_kind: SUB_ERR004_MAY_NIL,
            }]
        );
    }

    #[test]
    fn err005_deferred_close_sees_subsequent_write() {
        // defer wc.Close() registered in bb0 BEFORE wc.Write() in bb1 -> ERR005!
        let fn_ir = FuncIR {
            flags: 0,
            slot_flags: vec![0],
            tuples: vec![],
            blocks: vec![
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::UnhandledClose {
                        site_id: 77,
                        res_slot: 0,
                        is_defer: true,
                    }],
                    term: Terminator::Jump { target: 1 },
                },
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::MarkWritable {
                        site_id: 78,
                        res_slot: 0,
                    }],
                    term: Terminator::Return {
                        site_id: 79,
                        ret_slot: NONE_U32,
                        flags: 0,
                    },
                },
            ],
        };
        let res = solve_package(&[fn_ir], &mut || true).unwrap();
        assert_eq!(
            res.findings,
            vec![Finding {
                func_idx: 0,
                site_id: 77,
                rule_code: RULE_ERR005,
                sub_kind: SUB_ERR005_DEFER_CLOSE,
            }]
        );
    }

    #[test]
    fn semilattice_laws_and_height_bounds_hold_exhaustively() {
        let origins = [
            Origin::Bot,
            Origin::Safe,
            Origin::Single(0),
            Origin::Single(1),
            Origin::Single(2),
            Origin::UncheckedMix(0),
            Origin::UncheckedMix(1),
            Origin::UncheckedMix(2),
            Origin::Blanked(0),
            Origin::Blanked(1),
            Origin::Blanked(2),
        ];
        for &a in &origins {
            assert_eq!(Origin::Bot.join(a), a);
            assert_eq!(a.join(Origin::Bot), a);
            assert_eq!(a.join(a), a);
            for &b in &origins {
                assert_eq!(a.join(b), b.join(a), "Origin commutativity: {a:?}, {b:?}");
                for &c in &origins {
                    assert_eq!(
                        a.join(b).join(c),
                        a.join(b.join(c)),
                        "Origin associativity: {a:?}, {b:?}, {c:?}"
                    );
                }
            }
        }

        let nils = [
            NilVal::Bot,
            NilVal::MustNil,
            NilVal::NonNilChecked,
            NilVal::NonNil,
            NilVal::MayNil,
        ];
        for &a in &nils {
            assert_eq!(NilVal::Bot.join(a), a);
            assert_eq!(a.join(a), a);
            for &b in &nils {
                assert_eq!(a.join(b), b.join(a), "NilVal commutativity: {a:?}, {b:?}");
                // `relax_checked` must be monotone: if a <= b (i.e. a.join(b) == b),
                // then a.relax_checked() <= b.relax_checked().
                if a.join(b) == b {
                    let ra = a.relax_checked();
                    let rb = b.relax_checked();
                    assert_eq!(
                        ra.join(rb),
                        rb,
                        "NilVal::relax_checked non-monotone on {a:?} <= {b:?}"
                    );
                }
                for &c in &nils {
                    assert_eq!(
                        a.join(b).join(c),
                        a.join(b.join(c)),
                        "NilVal associativity: {a:?}, {b:?}, {c:?}"
                    );
                }
            }
        }

        let err_vars = [
            ErrVar::Bot,
            ErrVar::Clean,
            ErrVar::Unread(0),
            ErrVar::Unread(1),
            ErrVar::Unread(2),
        ];
        for &a in &err_vars {
            assert_eq!(ErrVar::Bot.join(a), a);
            assert_eq!(a.join(a), a);
            for &b in &err_vars {
                assert_eq!(a.join(b), b.join(a));
                for &c in &err_vars {
                    assert_eq!(a.join(b).join(c), a.join(b.join(c)));
                }
            }
        }

        let writables = [WriteMode::Bot, WriteMode::ReadOnly, WriteMode::Writable];
        for &a in &writables {
            assert_eq!(WriteMode::Bot.join(a), a);
            assert_eq!(a.join(a), a);
            for &b in &writables {
                assert_eq!(a.join(b), b.join(a));
                for &c in &writables {
                    assert_eq!(a.join(b).join(c), a.join(b.join(c)));
                }
            }
        }

        let links = [
            ErrLink::Bot,
            ErrLink::Tuple(0),
            ErrLink::Tuple(1),
            ErrLink::None,
        ];
        for &a in &links {
            assert_eq!(ErrLink::Bot.join(a), a);
            assert_eq!(a.join(a), a);
            for &b in &links {
                assert_eq!(a.join(b), b.join(a));
                for &c in &links {
                    assert_eq!(a.join(b).join(c), a.join(b.join(c)));
                }
            }
        }
    }

    #[test]
    fn state_allocation_failure_is_an_error_not_an_abort() {
        // A request no allocator can satisfy must come back as Err, never abort the process.
        assert_eq!(
            State::try_bottom(usize::MAX / 8).err(),
            Some(SolveError::AllocationFailed("block state"))
        );
        assert_eq!(
            try_vec::<SlotCell>(usize::MAX / 2, "x").err(),
            Some(SolveError::AllocationFailed("x"))
        );
        assert!(State::try_bottom(16).is_ok());
    }

    #[test]
    fn slot_cell_size_backs_the_budget_arithmetic() {
        // MAX_STATE_CELLS_PER_FUNC is derived from 4 states x 28 bytes per cell.
        assert!(std::mem::size_of::<SlotCell>() <= 28);
    }

    // ------------------------------------------------------------------------------------
    // Phase 1 cost, budgets and bounds. The Go twins are in internal/gusseterr/
    // phase1_cost_test.go and bounds_internal_test.go.
    // ------------------------------------------------------------------------------------

    fn term_return(site_id: u32, ret_slot: u32, flags: u8) -> Terminator {
        Terminator::Return {
            site_id,
            ret_slot,
            flags,
        }
    }

    fn call_op(site_id: u32, callee_idx: u32) -> OpIR {
        OpIR::CallConcretePtr {
            site_id,
            dst_slot: 0,
            callee_idx,
        }
    }

    fn one_block(ops: Vec<OpIR>, term: Terminator) -> Vec<BlockIR> {
        vec![BlockIR {
            phis: vec![],
            ops,
            term,
        }]
    }

    /// Function `i` returns the concrete error pointer returned by function `i + 1`; the last
    /// returns a literal nil, calling function 0 when `closed` (one cycle through all).
    fn chain(n: usize, closed: bool) -> Vec<FuncIR> {
        (0..n)
            .map(|i| {
                let (ops, term) = if i + 1 == n {
                    let ops = if closed { vec![call_op(1, 0)] } else { vec![] };
                    (ops, term_return(2, NONE_U32, RET_CONCRETE_LITERAL_NIL))
                } else {
                    (vec![call_op(1, (i + 1) as u32)], term_return(2, 0, 0))
                };
                FuncIR {
                    flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
                    slot_flags: vec![0],
                    tuples: vec![],
                    blocks: one_block(ops, term),
                }
            })
            .collect()
    }

    /// `n` functions: a hub calling every member of a chain of `n - 1` in which member `j`
    /// calls member `j + 1` (callers have the lower index) and the last returns a literal nil.
    /// The hub is index 0 when `hub_first`, else the last index. `closed` makes the last member
    /// call the hub too.
    fn hub_chain(n: usize, hub_first: bool, closed: bool) -> Vec<FuncIR> {
        let m = n - 1;
        let hub = if hub_first { 0 } else { n - 1 };
        let member = |j: usize| if hub_first { j + 1 } else { j };
        let mut funcs: Vec<Option<FuncIR>> = vec![None; n];
        let mut hub_ops: Vec<OpIR> = (0..m)
            .map(|j| call_op(j as u32 + 1, member(j) as u32))
            .collect();
        hub_ops.push(OpIR::AssignSlot {
            site_id: m as u32 + 1,
            dst_slot: 1,
            src_slot: NONE_U32,
            nil_hint: ASSIGN_NIL_NON_NIL,
            writable_hint: 0,
        });
        funcs[hub] = Some(FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![0, 0],
            tuples: vec![],
            blocks: one_block(hub_ops, term_return(1, 1, 0)),
        });
        for j in 0..m {
            let (ops, term) = if j + 1 == m {
                let ops = if closed {
                    vec![call_op(1, hub as u32)]
                } else {
                    vec![]
                };
                (ops, term_return(2, NONE_U32, RET_CONCRETE_LITERAL_NIL))
            } else {
                (vec![call_op(1, member(j + 1) as u32)], term_return(2, 0, 0))
            };
            funcs[member(j)] = Some(FuncIR {
                flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
                slot_flags: vec![0],
                tuples: vec![],
                blocks: one_block(ops, term),
            });
        }
        funcs
            .into_iter()
            .map(|f| f.expect("every index filled"))
            .collect()
    }

    fn package_size(funcs: &[FuncIR]) -> u64 {
        funcs
            .iter()
            .map(|f| {
                f.blocks.len() as u64 * (f.slot_flags.len() as u64).max(1)
                    + f.blocks.iter().map(|b| b.ops.len() as u64).sum::<u64>()
            })
            .sum()
    }

    /// A hub (index 0) calling every member of a chain of `n - 1` whose members call both
    /// neighbours and the hub, so the chain is a ladder of mutual calls in one cycle with the
    /// hub. Member `j` returns the result of member `j + 1`; the last returns a literal nil. A
    /// summary that flips at the last member reaches member `j - 1` along a back edge of the
    /// search each time, one sweep per member with the hub re-solved in every sweep: the shape
    /// the callee-first order inside a cycle does not help with and the work budget refuses.
    fn hub_ladder(n: usize) -> Vec<FuncIR> {
        let m = n - 1;
        let mut hub_ops: Vec<OpIR> = (1..=m).map(|j| call_op(j as u32, j as u32)).collect();
        hub_ops.push(OpIR::AssignSlot {
            site_id: m as u32 + 1,
            dst_slot: 1,
            src_slot: NONE_U32,
            nil_hint: ASSIGN_NIL_NON_NIL,
            writable_hint: 0,
        });
        let mut funcs = vec![FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![0, 0],
            tuples: vec![],
            blocks: one_block(hub_ops, term_return(1, 1, 0)),
        }];
        for j in 1..=m {
            let mut ops = vec![call_op(1, 0)];
            if j > 1 {
                ops.push(OpIR::CallConcretePtr {
                    site_id: 2,
                    dst_slot: 1,
                    callee_idx: j as u32 - 1,
                });
            }
            let term = if j < m {
                ops.push(OpIR::CallConcretePtr {
                    site_id: 3,
                    dst_slot: 2,
                    callee_idx: j as u32 + 1,
                });
                term_return(4, 2, 0)
            } else {
                term_return(4, NONE_U32, RET_CONCRETE_LITERAL_NIL)
            };
            funcs.push(FuncIR {
                flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
                slot_flags: vec![0, 0, 0],
                tuples: vec![],
                blocks: one_block(ops, term),
            });
        }
        funcs
    }

    fn limited(
        funcs: &[FuncIR],
        factor: u64,
        floor: u64,
    ) -> (Result<PackageResult, SolveError>, Phase1Stats) {
        let mut stats = Phase1Stats::default();
        let res = solve_package_limited(
            funcs,
            &mut || true,
            funcs.len() + 1,
            factor,
            floor,
            &mut stats,
        );
        (res, stats)
    }

    /// The phase-1 reference the callee-first order replaced: re-solve every unsettled function
    /// on every sweep until a sweep changes nothing.
    fn naive_summaries(funcs: &[FuncIR]) -> Vec<bool> {
        let mut nil: Vec<bool> = funcs
            .iter()
            .map(|f| {
                f.flags & FUNC_EXTERNAL_MAY_NIL != 0
                    || (f.flags & FUNC_RETURNS_CONCRETE_ERR_PTR != 0 && f.blocks.is_empty())
            })
            .collect();
        loop {
            let mut changed = false;
            for (i, f) in funcs.iter().enumerate() {
                if nil[i] || f.flags & FUNC_RETURNS_CONCRETE_ERR_PTR == 0 {
                    continue;
                }
                let solved = solve_function(i as u32, f, &nil, false, &mut || true).expect("solve");
                if solved.func_may_return_nil {
                    nil[i] = true;
                    changed = true;
                }
            }
            if !changed {
                return nil;
            }
        }
    }

    #[test]
    fn phase1_work_is_linear_on_hub_and_chain() {
        for hub_first in [true, false] {
            let mut prev = 0u64;
            for n in [4_096usize, 16_384, 65_536] {
                let funcs = hub_chain(n, hub_first, false);
                let (res, stats) = limited(&funcs, PHASE1_WORK_FACTOR, PHASE1_WORK_FLOOR);
                let res = res.expect("solves");
                let hub = if hub_first { 0 } else { n - 1 };
                for (i, &nil) in res.may_return_nil.iter().enumerate() {
                    assert_eq!(nil, i != hub, "n={n} func {i}");
                }
                assert_eq!(stats.solves, n as u64, "one solve per function at n={n}");
                assert_eq!(stats.work, package_size(&funcs), "n={n}");
                if prev != 0 {
                    assert!(
                        stats.work <= prev * 5,
                        "work grew {} -> {}",
                        prev,
                        stats.work
                    );
                }
                prev = stats.work;
            }
        }
    }

    #[test]
    fn phase1_matches_the_naive_solver_on_random_call_graphs() {
        let mut seed = 0x9e37_79b9_7f4a_7c15u64;
        let mut next = move || {
            seed ^= seed << 13;
            seed ^= seed >> 7;
            seed ^= seed << 17;
            seed
        };
        let mut flipped = 0usize;
        for _ in 0..400 {
            let n = 1 + (next() % 60) as usize;
            let funcs: Vec<FuncIR> = (0..n)
                .map(|_| {
                    let flags = match next() % 8 {
                        0 => FUNC_RETURNS_CONCRETE_ERR_PTR | FUNC_EXTERNAL_MAY_NIL,
                        1 => 0,
                        _ => FUNC_RETURNS_CONCRETE_ERR_PTR,
                    };
                    let calls = (next() % 4) as usize;
                    let mut ops: Vec<OpIR> = (0..calls)
                        .map(|k| call_op(k as u32 + 1, (next() % n as u64) as u32))
                        .collect();
                    let nil_ret = next() % 6 == 0;
                    if nil_ret {
                        ops.clear();
                    }
                    let term = if nil_ret {
                        term_return(9, NONE_U32, RET_CONCRETE_LITERAL_NIL)
                    } else {
                        term_return(9, 0, 0)
                    };
                    FuncIR {
                        flags,
                        slot_flags: vec![0],
                        tuples: vec![],
                        blocks: if flags & FUNC_EXTERNAL_MAY_NIL != 0 {
                            vec![]
                        } else {
                            one_block(ops, term)
                        },
                    }
                })
                .collect();
            let want = naive_summaries(&funcs);
            flipped += want.iter().filter(|&&b| b).count();
            let got = solve_package(&funcs, &mut || true).expect("solves");
            assert_eq!(got.may_return_nil, want, "n={n}");
        }
        assert!(
            flipped > 400,
            "the random graphs flipped only {flipped} summaries"
        );
    }

    #[test]
    fn phase1_shapes_edge_cases() {
        // Empty and single function.
        assert!(solve_package(&[], &mut || true)
            .expect("empty")
            .may_return_nil
            .is_empty());
        let single = chain(1, false);
        assert_eq!(
            solve_package(&single, &mut || true)
                .expect("single")
                .may_return_nil,
            [true]
        );
        // Self-recursive: calls itself on one path and returns nil on none, so it stays non-nil.
        let selfrec = vec![FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![0],
            tuples: vec![],
            blocks: one_block(vec![call_op(1, 0)], term_return(2, 0, 0)),
        }];
        assert_eq!(naive_summaries(&selfrec), [false]);
        assert_eq!(
            solve_package(&selfrec, &mut || true)
                .expect("self")
                .may_return_nil,
            [false]
        );
        // One cycle through every function, a chain in both directions, hubs in all orders.
        for funcs in [
            chain(1_000, true),
            chain(1_000, false),
            hub_chain(600, true, false),
            hub_chain(600, false, false),
            hub_chain(60, true, true),
            hub_chain(60, false, true),
        ] {
            let got = solve_package(&funcs, &mut || true).expect("solves");
            assert_eq!(got.may_return_nil, naive_summaries(&funcs));
        }
        // Chain length exactly at MAX_FUNCS: a hub calling all of it is solved once each.
        let funcs = hub_chain(MAX_FUNCS, true, false);
        let (res, stats) = limited(&funcs, PHASE1_WORK_FACTOR, PHASE1_WORK_FLOOR);
        assert!(res.is_ok());
        assert_eq!(stats.solves, MAX_FUNCS as u64);
    }

    #[test]
    fn closed_hub_and_chain_is_linear() {
        // f0dea98 re-solved the hub once per member (quadratic); 65e2cd4 refused this package
        // from n = 1,024 because it ordered the cycle by function index. Callee-first order inside
        // the cycle solves the hub twice, at any size.
        for hub_first in [true, false] {
            for n in [1_024usize, 4_096, 16_384] {
                let funcs = hub_chain(n, hub_first, true);
                let (res, stats) = limited(&funcs, PHASE1_WORK_FACTOR, PHASE1_WORK_FLOOR);
                res.expect("solves");
                let ratio = stats.work as f64 / package_size(&funcs) as f64;
                assert!(
                    ratio < 3.0 && stats.solves <= 2 * n as u64,
                    "hub_first={hub_first} n={n}: {} solves, work/size {ratio:.2}",
                    stats.solves
                );
            }
        }
    }

    #[test]
    fn hub_ladder_fails_closed_after_bounded_work() {
        let funcs = hub_ladder(4_096);
        let size = package_size(&funcs);
        let (res, stats) = limited(&funcs, PHASE1_WORK_FACTOR, PHASE1_WORK_FLOOR);
        let budget = (size * PHASE1_WORK_FACTOR).max(PHASE1_WORK_FLOOR);
        assert_eq!(
            res.unwrap_err(),
            SolveError::WorkBudgetExceeded(stats.work, budget)
        );
        assert!(stats.work > budget && stats.work <= budget + size);
        // Through the public entry point and the frame path too.
        assert!(matches!(
            solve_package(&funcs, &mut || true),
            Err(SolveError::WorkBudgetExceeded(_, _))
        ));
        let frame = encode_package(&funcs);
        assert!(matches!(
            solve_frame(&frame, || true),
            Err(SolveError::WorkBudgetExceeded(_, _))
        ));
    }

    #[test]
    fn phase1_budget_boundary_and_message() {
        // Nine solves of two work units each: the last function is external, as in the Go twin.
        let mut funcs = chain(10, false);
        funcs[9] = FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR | FUNC_EXTERNAL_MAY_NIL,
            slot_flags: vec![],
            tuples: vec![],
            blocks: vec![],
        };
        let (res, stats) = limited(&funcs, 0, 18);
        assert!(res.is_ok());
        assert_eq!(stats.work, 18);
        let (res, _) = limited(&funcs, 0, 17);
        let err = res.unwrap_err();
        assert_eq!(err, SolveError::WorkBudgetExceeded(18, 17));
        assert_eq!(
            err.to_string(),
            format!("phase-1 work budget exceeded: 18 work units against a budget of 17 (ops plus blocks x max(slots, 1) of every function solved; the budget is {PHASE1_WORK_FACTOR} times the package's total of that, at least {PHASE1_WORK_FLOOR})")
        );
        assert!(
            limited(&funcs, 2, 1).0.is_ok(),
            "factor x size beats a lower floor"
        );
        assert!(limited(&funcs, 0, 0).0.is_err());
        assert!(limited(&[], 0, 0).0.is_ok());
    }

    #[test]
    fn phase1_polls_cancellation_per_component_and_sweep() {
        for funcs in [
            chain(40, false),
            chain(25, true),
            hub_chain(30, true, false),
            hub_chain(12, true, true),
        ] {
            let mut polls = 0usize;
            let mut stats = Phase1Stats::default();
            solve_package_limited(
                &funcs,
                &mut || {
                    polls += 1;
                    true
                },
                funcs.len() + 1,
                PHASE1_WORK_FACTOR,
                PHASE1_WORK_FLOOR,
                &mut stats,
            )
            .expect("solves");
            assert!(
                polls as u64 >= stats.sccs + stats.sweeps + stats.solves,
                "{polls} polls, {stats:?}"
            );
            for fail_at in 1..=polls {
                let mut calls = 0usize;
                let res = solve_package(&funcs, &mut || {
                    calls += 1;
                    calls < fail_at
                });
                assert_eq!(
                    res,
                    Err(SolveError::Cancelled),
                    "cancel at poll {fail_at} of {polls}"
                );
            }
        }
    }

    #[test]
    fn call_sccs_is_iterative_and_callees_first() {
        const N: usize = 2_000_000;
        let mut callers: Vec<Vec<u32>> = (0..N)
            .map(|v| if v == 0 { vec![] } else { vec![(v - 1) as u32] })
            .collect();
        let active = vec![true; N];
        let (members, ends) = call_sccs(&callers, &active, &mut || true).expect("search");
        assert_eq!((members.len(), ends.len()), (N, N));
        // Function 0 calls 1 calls 2 ...: callees first, so N - 1 is solved first and 0 last.
        assert_eq!((members[0], members[N - 1]), ((N - 1) as u32, 0));
        callers[0] = vec![(N - 1) as u32];
        let (members, ends) = call_sccs(&callers, &active, &mut || true).expect("search");
        assert_eq!((members.len(), ends.len()), (N, 1));
    }

    fn model_funcs() -> Vec<FuncIR> {
        let mut funcs = chain(20, true);
        funcs.push(FuncIR {
            flags: 0,
            slot_flags: vec![0, 1, 0, 1, 0, 1, 0],
            tuples: vec![
                TupleMeta {
                    err_slot: 1,
                    call_site_id: 1,
                    flags: 0,
                },
                TupleMeta {
                    err_slot: 3,
                    call_site_id: 2,
                    flags: 0,
                },
            ],
            blocks: vec![
                BlockIR {
                    phis: vec![],
                    ops: vec![OpIR::BindTuple {
                        site_id: 1,
                        tuple_id: 0,
                        err_slot: 1,
                        companions: vec![2, 4],
                    }],
                    term: Terminator::Jump { target: 1 },
                },
                BlockIR {
                    phis: vec![PhiIR {
                        dst_slot: 6,
                        incoming: vec![(0, 2), (0, 4)],
                    }],
                    ops: vec![
                        OpIR::UnhandledErr { site_id: 3 },
                        OpIR::ObserveErr {
                            site_id: 4,
                            slot: 1,
                        },
                    ],
                    term: Terminator::Stop,
                },
            ],
        });
        funcs
    }

    #[test]
    fn decoded_bound_exact_and_one_over() {
        let funcs = model_funcs();
        let frame = encode_package(&funcs);
        let cost = decoded_bytes(&funcs);
        assert_eq!(
            decode_package_bounded(&frame, cost)
                .expect("at the bound")
                .len(),
            funcs.len()
        );
        assert_eq!(
            decode_package_bounded(&frame, cost - 1).err(),
            Some(SolveError::LimitExceeded("decoded_bytes"))
        );
        assert!(decode_package(&frame).is_ok());
        assert_eq!(decoded_bytes(&[]), 0);
    }

    #[test]
    fn hostile_counts_are_refused_before_they_allocate() {
        let mut f = REQ_MAGIC.to_vec();
        f.extend_from_slice(&1u32.to_le_bytes());
        f.push(0);
        for v in [0u32, 0, 1] {
            f.extend_from_slice(&v.to_le_bytes()); // slots, tuples, blocks
        }
        f.push(TERM_STOP);
        f.extend_from_slice(&0u32.to_le_bytes()); // phis
        f.extend_from_slice(&(MAX_OPS_PER_BLOCK as u32).to_le_bytes());
        let claimed =
            DECODED_FUNC_BYTES + DECODED_BLOCK_BYTES + MAX_OPS_PER_BLOCK as u64 * DECODED_OP_BYTES;
        assert_eq!(
            decode_package_bounded(&f, claimed).err(),
            Some(SolveError::Truncated)
        );
        assert_eq!(
            decode_package_bounded(&f, claimed - 1).err(),
            Some(SolveError::LimitExceeded("decoded_bytes"))
        );
        let mut budget = DecodeBudget {
            used: 0,
            max: u64::MAX,
        };
        assert!(budget.charge(u64::MAX, 2).is_err());
        let mut budget = DecodeBudget {
            used: u64::MAX - 1,
            max: u64::MAX,
        };
        assert!(budget.charge(2, 1).is_err());
    }

    #[test]
    fn decoded_size_model_covers_the_rust_records() {
        use std::mem::size_of;
        assert!(size_of::<FuncIR>() as u64 <= DECODED_FUNC_BYTES);
        assert!(size_of::<TupleMeta>() as u64 <= DECODED_TUPLE_BYTES);
        assert!(size_of::<BlockIR>() as u64 <= DECODED_BLOCK_BYTES);
        assert!(size_of::<PhiIR>() as u64 <= DECODED_PHI_BYTES);
        assert!(size_of::<(u32, u32)>() as u64 <= DECODED_PHI_EDGE_BYTES);
        assert!(size_of::<OpIR>() as u64 <= DECODED_OP_BYTES);
        assert!(size_of::<u32>() as u64 <= DECODED_COMPANION_BYTES);
    }

    #[test]
    fn response_cap_is_refused_before_encoding_with_the_native_text() {
        let frame = encode_package(&chain(10, false));
        let full = solve_frame(&frame, || true).expect("solves");
        assert_eq!(
            solve_frame_bounded(&frame, || true, full.len()).expect("at the bound"),
            full
        );
        let err = solve_frame_bounded(&frame, || true, full.len() - 1).unwrap_err();
        assert_eq!(
            err,
            SolveError::ResponseTooLarge(full.len(), full.len() - 1)
        );
        assert_eq!(
            err.to_string(),
            format!(
                "output {} bytes exceeds maximum {} bytes",
                full.len(),
                full.len() - 1
            )
        );
        assert_eq!(MAX_RESPONSE_BYTES, 1 << 30);
    }

    /// The decoded-size cost, frame length and phase-1 package size of `hub_chain(100, true,
    /// false)`: the same numbers the Go test `TestDecodedModelGoldenMatchesRust` pins for
    /// `hubChain(100, true, true, false)`, so both engines charge and refuse at exactly the same
    /// modelled byte count.
    #[test]
    fn model_matches_the_go_golden() {
        let funcs = hub_chain(100, true, false);
        let frame = encode_package(&funcs);
        assert_eq!(
            (decoded_bytes(&funcs), frame.len(), package_size(&funcs)),
            (30_389, 5_785, 299)
        );
        assert!(decode_package_bounded(&frame, 30_389).is_ok());
        assert_eq!(
            decode_package_bounded(&frame, 30_388).err(),
            Some(SolveError::LimitExceeded("decoded_bytes"))
        );
    }

    #[test]
    fn refusals_name_their_bound() {
        for (what, bound) in [
            ("frame_bytes", MAX_FRAME_BYTES as u64),
            ("decoded_bytes", MAX_DECODED_BYTES),
            ("num_funcs", MAX_FUNCS as u64),
            ("num_slots", MAX_SLOTS as u64),
            ("num_tuples", MAX_TUPLES as u64),
            ("num_blocks", MAX_BLOCKS as u64),
            ("state cells (blocks x slots)", MAX_STATE_CELLS_PER_FUNC),
            ("num_phis", MAX_PHIS_PER_BLOCK as u64),
            ("phi_arity", MAX_PHI_ARITY as u64),
            ("num_ops", MAX_OPS_PER_BLOCK as u64),
            ("companions", MAX_COMPANIONS as u64),
        ] {
            assert_eq!(
                SolveError::LimitExceeded(what).to_string(),
                format!("GSE1 limit exceeded: {what} over the bound of {bound}")
            );
        }
        assert!(SolveError::IterationBudgetExceeded(3)
            .to_string()
            .contains(&format!("(bound {MAX_ITERATIONS_PER_FUNC} steps)")));
        assert_eq!(
            SolveError::LimitExceeded("decoded_bytes").wire_message(),
            format!(
                "decoded_bytes_exceeded: GSE1 limit exceeded: decoded_bytes over the bound of {MAX_DECODED_BYTES}"
            )
        );
    }

    #[test]
    fn phase1_work_unit_counts_state_cells() {
        for (blocks, slots) in [(2048usize, 1usize), (2048, 4096), (7, 0), (1, 3)] {
            let mut bl: Vec<BlockIR> = (0..blocks)
                .map(|i| BlockIR {
                    phis: vec![],
                    ops: vec![],
                    term: Terminator::Jump {
                        target: i as u32 + 1,
                    },
                })
                .collect();
            bl[blocks - 1].term = term_return(1, NONE_U32, RET_CONCRETE_LITERAL_NIL);
            let f = FuncIR {
                flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
                slot_flags: vec![0; slots],
                tuples: vec![],
                blocks: bl,
            };
            let (res, stats) = limited(
                std::slice::from_ref(&f),
                PHASE1_WORK_FACTOR,
                PHASE1_WORK_FLOOR,
            );
            res.expect("solves");
            let want = blocks as u64 * (slots as u64).max(1);
            assert_eq!(stats.work, want, "{blocks} blocks x {slots} slots");
            assert_eq!(package_size(std::slice::from_ref(&f)), want);
        }
    }
}
