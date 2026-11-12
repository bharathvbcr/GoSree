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
/// bits, caller lists, the phase-1 worklists), and phase 1 costs O((functions + call edges) x
/// log functions) plus one solve per function and per callee flip, so the bound is set by real
/// packages, not by solver cost: a package with its tests has reached 18,973 (ScholarLM
/// `internal/wisdev`). `MaxFuncs` in `internal/gusseterr/wire.go` is the mirror.
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
    Cancelled,
}

impl fmt::Display for SolveError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            SolveError::BadMagic => write!(f, "invalid GSE1 frame magic"),
            SolveError::Truncated => write!(f, "truncated GSE1 frame"),
            SolveError::TrailingBytes(n) => write!(f, "GSE1 frame has {n} trailing bytes"),
            SolveError::LimitExceeded(what) => write!(f, "GSE1 limit exceeded: {what}"),
            SolveError::InvalidEnum(what, v) => write!(f, "invalid {what} tag: {v}"),
            SolveError::IndexOutOfBounds(what, idx, max) => {
                write!(f, "{what} index {idx} out of bounds (len {max})")
            }
            SolveError::IterationBudgetExceeded(fn_id) => {
                write!(f, "CFG worklist iteration budget exceeded in func {fn_id}")
            }
            SolveError::CallgraphBudgetExceeded(iters) => write!(
                f,
                "call-graph summary fixpoint did not converge within {iters} iterations"
            ),
            SolveError::AllocationFailed(what) => {
                write!(f, "solver allocation failed: {what}")
            }
            SolveError::Cancelled => write!(f, "solver cancelled"),
        }
    }
}

impl std::error::Error for SolveError {}

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

/// Decodes a `GSE1` binary request frame into validated `FuncIR` records.
pub fn decode_package(frame: &[u8]) -> Result<Vec<FuncIR>, SolveError> {
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

    let mut funcs = Vec::with_capacity(num_funcs.min(256));
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

        let slot_flags = cur.read_exact(num_slots)?.to_vec();

        let mut tuples = Vec::with_capacity(num_tuples.min(128));
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

        let mut blocks = Vec::with_capacity(num_blocks.min(128));
        for _ in 0..num_blocks {
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
            let mut phis = Vec::with_capacity(num_phis.min(64));
            for _ in 0..num_phis {
                let dst_slot = cur.read_u32()?;
                check_slot(dst_slot, num_slots_u32, false)?;
                let arity = cur.read_u16()? as usize;
                if arity > MAX_PHI_ARITY {
                    return Err(SolveError::LimitExceeded("phi_arity"));
                }
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
            let mut ops = Vec::with_capacity(num_ops.min(128));
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

/// Encodes a `PackageResult` into a deterministic `GSR1` binary response frame.
pub fn encode_result(res: &PackageResult) -> Vec<u8> {
    let cap = 4 + 4 + res.may_return_nil.len() + 4 + res.findings.len() * 12;
    let mut out = Vec::with_capacity(cap);
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
pub fn solve_frame<F>(frame: &[u8], mut check_cancel: F) -> Result<Vec<u8>, SolveError>
where
    F: FnMut() -> bool,
{
    let funcs = decode_package(frame)?;
    let res = solve_package(&funcs, &mut check_cancel)?;
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

/// `solve_package` with an explicit call-graph sweep cap (the public entry point passes
/// `funcs.len() + 1`, the proven convergence bound; tests pass a small cap to reach the error).
pub fn solve_package_capped<F>(
    funcs: &[FuncIR],
    check_cancel: &mut F,
    max_callgraph_iters: usize,
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
    // The summaries only ever flip false -> true, so this converges in at most one changing
    // sweep per function plus one confirming sweep. A function is re-solved only after one of
    // its callees flipped (its result depends on nothing else), so a long chain costs one solve
    // per function and per call edge, not one per sweep. Missing the cap would export
    // optimistic (too-small) summaries that hide ERR004 findings, so it is an error.
    //
    // A sweep visits the dirty functions in index order. It used to scan every index on every
    // sweep, which made a chain whose callers precede their callees cost one scan of the whole
    // package per function (quadratic: 0.3 s at 16,384 functions, minutes at 262,144). The
    // dirty functions are kept in two min-heaps instead: `this_sweep` holds those still ahead of
    // the sweep's position and `next_sweep` those behind it, which is the order the scan visited
    // them in, so the sweeps, the solves and their order are unchanged and the cost is
    // O((solves + call edges) x log functions). `dirty[i]` is true while `i` is in a heap, or
    // when `i` can never be solved again (its summary is already may-nil or it returns no
    // concrete error pointer), so no index is queued twice.
    let mut callers: Vec<Vec<u32>> = vec![Vec::new(); n_funcs];
    for (idx, fn_ir) in funcs.iter().copied().enumerate() {
        for block in &fn_ir.blocks {
            for op in &block.ops {
                if let OpIR::CallConcretePtr { callee_idx, .. } = op {
                    let v = &mut callers[*callee_idx as usize];
                    if v.last() != Some(&(idx as u32)) {
                        v.push(idx as u32);
                    }
                }
            }
        }
    }
    let mut dirty = vec![true; n_funcs];
    let mut this_sweep: BinaryHeap<Reverse<u32>> = (0..n_funcs as u32).map(Reverse).collect();
    let mut next_sweep: BinaryHeap<Reverse<u32>> = BinaryHeap::new();
    let mut converged = false;
    for _iter in 0..max_callgraph_iters {
        if !check_cancel() {
            return Err(SolveError::Cancelled);
        }
        let mut any_changed = false;
        while let Some(Reverse(idx32)) = this_sweep.pop() {
            let idx = idx32 as usize;
            let fn_ir = funcs[idx];
            if may_return_nil[idx]
                || !dirty[idx]
                || (fn_ir.flags & FUNC_RETURNS_CONCRETE_ERR_PTR == 0)
            {
                continue;
            }
            dirty[idx] = false;
            if fn_ir.blocks.is_empty() {
                may_return_nil[idx] = true;
                any_changed = true;
                continue;
            }
            let solved = solve_function(idx32, fn_ir, &may_return_nil, false, check_cancel)?;
            if solved.func_may_return_nil {
                may_return_nil[idx] = true;
                any_changed = true;
                for &c in &callers[idx] {
                    if dirty[c as usize] {
                        continue;
                    }
                    dirty[c as usize] = true;
                    if c > idx32 {
                        this_sweep.push(Reverse(c));
                    } else {
                        next_sweep.push(Reverse(c));
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
}
