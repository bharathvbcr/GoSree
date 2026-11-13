//! Adversarial tests for ft-5e61 against the Rust engine: cancellation on a 262,144-function
//! chain, per-SCC sweep caps against the Go oracle's numbers, hostile decoder claims at every
//! bound, and what the decoder's charge means in live heap bytes.

use gosree_errsolve::*;
use std::alloc::{GlobalAlloc, Layout, System};
use std::sync::atomic::{AtomicUsize, Ordering};

struct Counting;
static LIVE: AtomicUsize = AtomicUsize::new(0);
static TOTAL: AtomicUsize = AtomicUsize::new(0);

// SAFETY: forwards every call to the system allocator unchanged and only counts sizes.
unsafe impl GlobalAlloc for Counting {
    unsafe fn alloc(&self, l: Layout) -> *mut u8 {
        LIVE.fetch_add(l.size(), Ordering::Relaxed);
        TOTAL.fetch_add(l.size(), Ordering::Relaxed);
        System.alloc(l)
    }
    unsafe fn dealloc(&self, p: *mut u8, l: Layout) {
        LIVE.fetch_sub(l.size(), Ordering::Relaxed);
        System.dealloc(p, l)
    }
    unsafe fn realloc(&self, p: *mut u8, l: Layout, new: usize) -> *mut u8 {
        if new >= l.size() {
            LIVE.fetch_add(new - l.size(), Ordering::Relaxed);
            TOTAL.fetch_add(new - l.size(), Ordering::Relaxed);
        } else {
            LIVE.fetch_sub(l.size() - new, Ordering::Relaxed);
        }
        System.realloc(p, l, new)
    }
}

#[global_allocator]
static A: Counting = Counting;

/// The allocator counters are process-wide, so a test that reads them must not run beside one that
/// allocates (EXPECTATION CHANGED, ft-5e61 fix D: `decoded_heap_against_charge` measured the
/// allocations of the 262,144-function chain test running in parallel and failed in full runs
/// though it passes alone). Every test takes this lock.
static SERIAL: std::sync::Mutex<()> = std::sync::Mutex::new(());

fn call_ret(callee: u32) -> FuncIR {
    FuncIR {
        flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
        slot_flags: vec![0],
        tuples: vec![],
        blocks: vec![BlockIR {
            phis: vec![],
            ops: vec![OpIR::CallConcretePtr {
                site_id: 1,
                dst_slot: 0,
                callee_idx: callee,
            }],
            term: Terminator::Return {
                site_id: 2,
                ret_slot: 0,
                flags: 0,
            },
        }],
    }
}

fn nil_source(callee: Option<u32>) -> FuncIR {
    FuncIR {
        flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
        slot_flags: vec![0],
        tuples: vec![],
        blocks: vec![BlockIR {
            phis: vec![],
            ops: callee
                .map(|c| OpIR::CallConcretePtr {
                    site_id: 1,
                    dst_slot: 0,
                    callee_idx: c,
                })
                .into_iter()
                .collect(),
            term: Terminator::Return {
                site_id: 2,
                ret_slot: NONE_U32,
                flags: RET_CONCRETE_LITERAL_NIL,
            },
        }],
    }
}

/// i returns i+1's result; the last returns a literal nil and calls function 0.
fn call_cycle(n: usize) -> Vec<FuncIR> {
    let mut v: Vec<FuncIR> = (0..n - 1).map(|i| call_ret(i as u32 + 1)).collect();
    v.push(nil_source(Some(0)));
    v
}

fn call_chain(n: usize) -> Vec<FuncIR> {
    let mut v: Vec<FuncIR> = (0..n - 1).map(|i| call_ret(i as u32 + 1)).collect();
    v.push(nil_source(None));
    v
}

fn min_cap(funcs: &[FuncIR], limit: usize) -> usize {
    let want = solve_package(funcs, &mut || true).expect("solve");
    let mut first = 0;
    for c in 1..=limit {
        match solve_package_capped(funcs, &mut || true, c) {
            Ok(r) => {
                assert_eq!(r, want, "cap {c} converged to a different result");
                if first == 0 {
                    first = c;
                }
            }
            Err(SolveError::CallgraphBudgetExceeded(_)) => {
                assert_eq!(first, 0, "cap {c} fails after cap {first} passed");
            }
            Err(e) => panic!("cap {c}: {e}"),
        }
    }
    first
}

/// The same numbers the Go oracle's TestAdv5e61CapManySmallSCCsAndExactSCC pins: the engines
/// share the cap semantics (per component), including the exact-cap and chained-pair cases.
#[test]
fn per_scc_cap_matches_the_oracle() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    // EXPECTATION CHANGED (ft-5e61 fix A): call_cycle(9) needed a cap of 10 in function-index order
    // (nine changing sweeps, one confirming). In the callee-first order inside the cycle the nil
    // reaches every function in sweep 1 but function 0, which flips in sweep 2: it needs 3.
    assert_eq!(min_cap(&call_cycle(9), 12), 3);
    assert!(matches!(
        solve_package_capped(&call_cycle(9), &mut || true, 2),
        Err(SolveError::CallgraphBudgetExceeded(2))
    ));
    // 40 pairs A_i <-> B_i, A_i returns A_{i+1}, B_i returns A_i, the last A is a nil source.
    let k = 40usize;
    let mut v: Vec<FuncIR> = Vec::new();
    for i in 0..k {
        let (a, b) = (2 * i as u32, 2 * i as u32 + 1);
        let next_a = 2 * (i as u32 + 1);
        let two = |ret: u32, x: u32, y: u32| FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![0, 0],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops: vec![
                    OpIR::CallConcretePtr {
                        site_id: 1,
                        dst_slot: 0,
                        callee_idx: x,
                    },
                    OpIR::CallConcretePtr {
                        site_id: 2,
                        dst_slot: 1,
                        callee_idx: y,
                    },
                ],
                term: Terminator::Return {
                    site_id: 3,
                    ret_slot: ret,
                    flags: 0,
                },
            }],
        };
        v.push(if i == k - 1 {
            nil_source(Some(b))
        } else {
            two(1, b, next_a)
        });
        v.push(two(0, a, a));
    }
    assert_eq!(
        min_cap(&v, 6),
        2,
        "chained pairs need a cap of 2 per component"
    );
}

/// Cancellation: a 262,144-function chain polls once per component and the solve stops at the
/// first refusal, wherever it comes (in the component search or the sweeps).
#[test]
fn cancellation_on_a_max_funcs_chain() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    let funcs = call_chain(MAX_FUNCS);
    let mut calls = 0usize;
    solve_package(&funcs, &mut || {
        calls += 1;
        true
    })
    .expect("chain solves");
    assert!(
        calls >= MAX_FUNCS,
        "{calls} polls for {MAX_FUNCS} components"
    );
    let mut points = vec![1, 2, 3, 10, 100, calls / 4, calls / 2, calls - 100, calls];
    points.retain(|&p| p >= 1);
    for at in points {
        let mut n = 0usize;
        let mut after = 0usize;
        let r = solve_package(&funcs, &mut || {
            n += 1;
            if n >= at {
                after += 1;
                return false;
            }
            true
        });
        assert_eq!(
            r,
            Err(SolveError::Cancelled),
            "cancel at poll {at} of {calls}"
        );
        assert_eq!(
            after, 1,
            "cancel at poll {at}: polled {after} times after cancelling"
        );
    }
}

/// A giant cycle is one component: how many polls does a full solve make against how many
/// solves? (Prints the numbers; the sweep polls once, solve_function every 64 steps.)
#[test]
fn polls_inside_a_giant_cycle_sweep() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    let funcs = call_cycle(20_000);
    let mut calls = 0usize;
    solve_package(&funcs, &mut || {
        calls += 1;
        true
    })
    .expect("cycle solves");
    println!("cycle of 20000: {calls} polls");
}

fn le32(vs: &[u32]) -> Vec<u8> {
    vs.iter().flat_map(|v| v.to_le_bytes()).collect()
}

fn hostile_frames() -> Vec<(&'static str, Vec<u8>)> {
    let head = |n: u32| {
        let mut f = b"GSE1".to_vec();
        f.extend(n.to_le_bytes());
        f
    };
    let mut max_ops = head(1);
    max_ops.push(0);
    max_ops.extend(le32(&[0, 0, 1]));
    max_ops.push(TERM_STOP);
    max_ops.extend(le32(&[0, MAX_OPS_PER_BLOCK as u32]));
    let mut max_phis = head(1);
    max_phis.push(0);
    max_phis.extend(le32(&[1, 0, 1]));
    max_phis.push(TERM_STOP);
    max_phis.extend(le32(&[MAX_PHIS_PER_BLOCK as u32]));
    let mut slots = head(1);
    slots.push(0);
    slots.extend(le32(&[MAX_SLOTS as u32, 0, 0]));
    let mut tuples = head(1);
    tuples.push(0);
    tuples.extend(le32(&[0, MAX_TUPLES as u32, 0]));
    let mut blocks = head(1);
    blocks.push(0);
    blocks.extend(le32(&[0, 0, MAX_BLOCKS as u32]));
    let mut funcs = head(MAX_FUNCS as u32);
    funcs.push(0);
    funcs.extend(le32(&[0, 0, 0]));
    vec![
        ("max ops", max_ops),
        ("max phis", max_phis),
        ("max slots", slots),
        ("max tuples", tuples),
        ("max blocks", blocks),
        ("max funcs", funcs),
        ("over max funcs", head(MAX_FUNCS as u32 + 1)),
    ]
}

/// Hostile claims are refused or truncated at every injected bound, including u64::MAX where the
/// charge arithmetic could wrap, and allocate almost nothing.
#[test]
fn hostile_claims_at_every_bound() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    for (name, f) in hostile_frames() {
        for bound in [0u64, 1, 95, 96, 1 << 20, MAX_DECODED_BYTES, u64::MAX] {
            let before = TOTAL.load(Ordering::Relaxed);
            let r = decode_package_bounded(&f, bound);
            let grew = TOTAL.load(Ordering::Relaxed) - before;
            assert!(r.is_err(), "{name} bound {bound}: hostile frame accepted");
            assert!(
                grew < 8 << 20,
                "{name} bound {bound}: allocated {grew} bytes"
            );
        }
    }
}

/// The charge against the live heap: a block of 129 ops is built with capacity 128 and grows once
/// (a Vec doubles), so the live decoded bytes are about twice what `decoded_bytes` charges.
#[test]
fn decoded_heap_against_charge() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    const BLOCKS: usize = 6000;
    const OPS: usize = 129;
    let blocks: Vec<BlockIR> = (0..BLOCKS)
        .map(|_| BlockIR {
            phis: vec![],
            ops: (0..OPS as u32)
                .map(|i| OpIR::UnhandledErr { site_id: i })
                .collect(),
            term: Terminator::Stop,
        })
        .collect();
    let f = FuncIR {
        flags: 0,
        slot_flags: vec![0],
        tuples: vec![],
        blocks,
    };
    let frame = encode_package(std::slice::from_ref(&f));
    let charge = decoded_bytes(std::slice::from_ref(&f));
    drop(f);
    let base = LIVE.load(Ordering::Relaxed);
    let got = decode_package(&frame).expect("decodes");
    let live = LIVE.load(Ordering::Relaxed) - base;
    drop(got);
    let ratio = live as f64 / charge as f64;
    println!(
        "frame {} bytes, charged {charge}, live decoded heap {live} ({ratio:.2}x)",
        frame.len()
    );
    assert!(
        ratio <= 1.25,
        "the live decoded heap is {ratio:.2}x what decoded_bytes charged ({live} vs {charge}): the {} byte bound admits about {:.1} GiB",
        MAX_DECODED_BYTES,
        ratio * 2.0
    );
}

/// The work-budget error text the Rust engine prints is the one the oracle prints (same numbers
/// for the same package), checked on a hub ladder built here. EXPECTATION CHANGED (ft-5e61 fix A):
/// this used a closed hub and chain, which the callee-first order inside a cycle now solves in
/// linear work; the hub ladder (every member calls both neighbours and the hub) is the cycle that
/// is still quadratic.
#[test]
fn budget_refusal_text_is_stable() {
    let _serial = SERIAL.lock().unwrap_or_else(|e| e.into_inner());
    let n = 2048usize;
    let m = n - 1;
    let mut hub_ops: Vec<OpIR> = (1..=m as u32)
        .map(|c| OpIR::CallConcretePtr {
            site_id: c,
            dst_slot: 0,
            callee_idx: c,
        })
        .collect();
    hub_ops.push(OpIR::AssignSlot {
        site_id: n as u32 + 1,
        dst_slot: 1,
        src_slot: NONE_U32,
        nil_hint: ASSIGN_NIL_NON_NIL,
        writable_hint: 0,
    });
    let ret = |slot: u32, flags: u8| Terminator::Return {
        site_id: 4,
        ret_slot: slot,
        flags,
    };
    let call = |site_id: u32, dst_slot: u32, callee_idx: u32| OpIR::CallConcretePtr {
        site_id,
        dst_slot,
        callee_idx,
    };
    let mut v = vec![FuncIR {
        flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
        slot_flags: vec![0, 0],
        tuples: vec![],
        blocks: vec![BlockIR {
            phis: vec![],
            ops: hub_ops,
            term: Terminator::Return {
                site_id: 1,
                ret_slot: 1,
                flags: 0,
            },
        }],
    }];
    for j in 1..=m as u32 {
        let mut ops = vec![call(1, 0, 0)];
        if j > 1 {
            ops.push(call(2, 1, j - 1));
        }
        let term = if (j as usize) < m {
            ops.push(call(3, 2, j + 1));
            ret(2, 0)
        } else {
            ret(NONE_U32, RET_CONCRETE_LITERAL_NIL)
        };
        v.push(FuncIR {
            flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
            slot_flags: vec![0, 0, 0],
            tuples: vec![],
            blocks: vec![BlockIR {
                phis: vec![],
                ops,
                term,
            }],
        });
    }
    let err = solve_package(&v, &mut || true).expect_err("hub ladder is refused");
    let text = err.to_string();
    assert!(text.starts_with("phase-1 work budget exceeded: "), "{text}");
    assert_eq!(err.code(), "work_budget_exceeded");
    assert!(err
        .wire_message()
        .starts_with("work_budget_exceeded: phase-1 work budget exceeded: "));
    println!("{text}");
}
