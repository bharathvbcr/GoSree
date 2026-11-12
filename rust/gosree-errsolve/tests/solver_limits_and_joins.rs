//! Solver budgets, fail-closed caps, phi-joined tuple identity, loop-phi re-queueing, and
//! decoder robustness against arbitrary frames.

use gosree_errsolve::*;

fn ret(site_id: u32, ret_slot: u32) -> Terminator {
    Terminator::Return {
        site_id,
        ret_slot,
        flags: 0,
    }
}

fn block(ops: Vec<OpIR>, term: Terminator) -> BlockIR {
    BlockIR {
        phis: vec![],
        ops,
        term,
    }
}

fn solve(funcs: &[FuncIR]) -> PackageResult {
    solve_package(funcs, &mut || true).expect("solve")
}

fn rules(res: &PackageResult) -> Vec<(u32, u8)> {
    res.findings
        .iter()
        .map(|f| (f.site_id, f.rule_code))
        .collect()
}

#[test]
fn decoder_rejects_function_over_the_state_cell_budget() {
    // 4,096 slots x 2,049 blocks = 8,392,704 cells > 8,388,608 (each factor is under its own limit).
    let n_blocks = 2_049;
    let f = FuncIR {
        flags: 0,
        slot_flags: vec![0; 4_096],
        tuples: vec![],
        blocks: (0..n_blocks)
            .map(|_| block(vec![], Terminator::Stop))
            .collect(),
    };
    let frame = encode_package(&[f]);
    assert_eq!(
        decode_package(&frame).err(),
        Some(SolveError::LimitExceeded("state cells (blocks x slots)"))
    );
    assert_eq!(
        solve_frame(&frame, || true),
        Err(SolveError::LimitExceeded("state cells (blocks x slots)"))
    );

    // Exactly on the budget is accepted: 4,096 x 2,048 = 2^23.
    let ok = FuncIR {
        flags: 0,
        slot_flags: vec![0; 4_096],
        tuples: vec![],
        blocks: (0..2_048)
            .map(|_| block(vec![], Terminator::Stop))
            .collect(),
    };
    let frame = encode_package(&[ok]);
    assert!(decode_package(&frame).is_ok());
}

/// Function `i` (for `i < n - 1`) returns the concrete error pointer returned by function
/// `i + 1`; the last function is external and may return nil. Visiting functions in index
/// order flips exactly one summary per sweep.
fn call_chain(n: usize) -> Vec<FuncIR> {
    let mut funcs = Vec::new();
    for i in 0..n {
        if i + 1 == n {
            funcs.push(FuncIR {
                flags: FUNC_RETURNS_CONCRETE_ERR_PTR | FUNC_EXTERNAL_MAY_NIL,
                slot_flags: vec![],
                tuples: vec![],
                blocks: vec![],
            });
        } else {
            funcs.push(FuncIR {
                flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
                slot_flags: vec![0],
                tuples: vec![],
                blocks: vec![block(
                    vec![OpIR::CallConcretePtr {
                        site_id: 1,
                        dst_slot: 0,
                        callee_idx: (i + 1) as u32,
                    }],
                    ret(2, 0),
                )],
            });
        }
    }
    funcs
}

#[test]
fn callgraph_cap_fails_closed_instead_of_exporting_optimistic_summaries() {
    let funcs = call_chain(10);
    let err = solve_package_capped(&funcs, &mut || true, 3).unwrap_err();
    assert_eq!(err, SolveError::CallgraphBudgetExceeded(3));
    assert!(err
        .to_string()
        .contains("did not converge within 3 iterations"));

    // With enough sweeps the same package converges and every summary is may-nil.
    let res = solve_package_capped(&funcs, &mut || true, 64).expect("converges");
    assert!(res.may_return_nil.iter().all(|&b| b));

    // Nine sweeps each flip one summary; a tenth, unchanged sweep confirms the fixpoint. A
    // fixpoint reached on the last permitted sweep without that confirmation is not trusted.
    assert_eq!(
        solve_package_capped(&funcs, &mut || true, 9).unwrap_err(),
        SolveError::CallgraphBudgetExceeded(9)
    );
    assert!(solve_package_capped(&funcs, &mut || true, 10).is_ok());
}

#[test]
fn public_entry_converges_on_chains_up_to_max_funcs() {
    // Callers before callees: one changing sweep per function. The cap is derived from the
    // function count, so valid chains of any length up to MAX_FUNCS converge.
    for n in [4_097, MAX_FUNCS] {
        let funcs = call_chain(n);
        let start = std::time::Instant::now();
        let res = solve_package(&funcs, &mut || true).expect("converges");
        eprintln!("chain n={n}: {:?}", start.elapsed());
        assert_eq!(res.may_return_nil.len(), n);
        assert!(res.may_return_nil.iter().all(|&b| b));
    }
}

/// The IR the lowering emits for
/// `if c { it, err = fetchA() } else { it, err = fetchB() }; if err != nil { return }; use(it)`:
/// each branch binds its own tuple into its own SSA slots and both are merged by phis.
fn two_branch_tuple(with_check: bool) -> FuncIR {
    // slots: 0 cond, 1 err0, 2 comp0, 3 err1, 4 comp1, 5 err phi, 6 comp phi
    let slot_flags = vec![0, SLOT_IS_ERROR, 0, SLOT_IS_ERROR, 0, SLOT_IS_ERROR, 0];
    let tuples = vec![
        TupleMeta {
            err_slot: 1,
            call_site_id: 10,
            flags: 0,
        },
        TupleMeta {
            err_slot: 3,
            call_site_id: 11,
            flags: 0,
        },
    ];
    let join_term = if with_check {
        Terminator::Cond {
            true_bb: 4,
            false_bb: 5,
            cond_kind: COND_NOT_NIL,
            cond_slot: 5,
        }
    } else {
        Terminator::Jump { target: 5 }
    };
    let mut join = block(vec![], join_term);
    join.phis = vec![
        PhiIR {
            dst_slot: 6,
            incoming: vec![(1, 2), (2, 4)],
        },
        PhiIR {
            dst_slot: 5,
            incoming: vec![(1, 1), (2, 3)],
        },
    ];
    let blocks = vec![
        block(
            vec![],
            Terminator::Cond {
                true_bb: 1,
                false_bb: 2,
                cond_kind: COND_OPAQUE,
                cond_slot: NONE_U32,
            },
        ),
        block(
            vec![OpIR::BindTuple {
                site_id: 10,
                tuple_id: 0,
                err_slot: 1,
                companions: vec![2],
            }],
            Terminator::Jump { target: 3 },
        ),
        block(
            vec![OpIR::BindTuple {
                site_id: 11,
                tuple_id: 1,
                err_slot: 3,
                companions: vec![4],
            }],
            Terminator::Jump { target: 3 },
        ),
        join,
        // bb4: err != nil -> return; bb5: use the companion.
        block(vec![], ret(20, NONE_U32)),
        block(
            vec![OpIR::UseCompanion {
                site_id: 30,
                slot: 6,
            }],
            ret(21, NONE_U32),
        ),
    ];
    // Without the check, bb3 jumps to bb5 and bb4 is unreachable; both are still valid blocks.
    FuncIR {
        flags: 0,
        slot_flags,
        tuples,
        blocks,
    }
}

#[test]
fn tuples_merged_by_a_phi_share_identity_so_one_check_promotes_the_companion() {
    let checked = solve(&[two_branch_tuple(true)]);
    assert!(
        rules(&checked).is_empty(),
        "one check after the join establishes err == nil for either call: {:?}",
        checked.findings
    );
    let unchecked = solve(&[two_branch_tuple(false)]);
    assert_eq!(rules(&unchecked), vec![(30, RULE_ERR002)]);
}

/// `e := &MyErr{}; for ... { e = lookup(k) }; return e` as the lowering emits it: the loop
/// header phi merges the pre-loop slot with the slot reassigned in the body, and the value that
/// may be nil reaches the header only through the back edge.
#[test]
fn loop_phi_over_error_value_requeues_the_header_when_the_back_edge_changes() {
    // slots: 0 e0 (non-nil literal), 1 e1 (maybe-nil call result), 2 header phi
    let slot_flags = vec![0, 0, 0];
    let mut header = block(
        vec![],
        Terminator::Cond {
            true_bb: 2,
            false_bb: 3,
            cond_kind: COND_OPAQUE,
            cond_slot: NONE_U32,
        },
    );
    header.phis = vec![PhiIR {
        dst_slot: 2,
        incoming: vec![(0, 0), (2, 1)],
    }];
    let blocks = vec![
        block(
            vec![OpIR::AssignSlot {
                site_id: 1,
                dst_slot: 0,
                src_slot: NONE_U32,
                nil_hint: ASSIGN_NIL_NON_NIL,
                writable_hint: 0,
            }],
            Terminator::Jump { target: 1 },
        ),
        header,
        block(
            vec![OpIR::CallConcretePtr {
                site_id: 2,
                dst_slot: 1,
                callee_idx: 1,
            }],
            Terminator::Jump { target: 1 },
        ),
        block(
            vec![OpIR::BoxConcreteErr {
                site_id: 3,
                ptr_slot: 2,
            }],
            ret(4, 2),
        ),
    ];
    let lookup = FuncIR {
        flags: FUNC_RETURNS_CONCRETE_ERR_PTR | FUNC_EXTERNAL_MAY_NIL,
        slot_flags: vec![],
        tuples: vec![],
        blocks: vec![],
    };
    let f = FuncIR {
        flags: FUNC_RETURNS_ERROR,
        slot_flags,
        tuples: vec![],
        blocks,
    };
    let res = solve(&[f, lookup]);
    assert_eq!(rules(&res), vec![(3, RULE_ERR004)]);
}

/// Deterministic xorshift so the test needs no dependency.
struct Rng(u64);
impl Rng {
    fn next(&mut self) -> u64 {
        self.0 ^= self.0 << 13;
        self.0 ^= self.0 >> 7;
        self.0 ^= self.0 << 17;
        self.0
    }
}

#[test]
fn decoder_never_panics_on_arbitrary_frames() {
    let mut rng = Rng(0x9e37_79b9_7f4a_7c15);
    // Pure noise, noise behind a valid magic, and mutations of a valid frame.
    let seed_frame = encode_package(&[two_branch_tuple(true), two_branch_tuple(false)]);
    let mut accepted = 0usize;
    for i in 0..20_000 {
        let frame: Vec<u8> = match i % 3 {
            0 => (0..(rng.next() % 200) as usize)
                .map(|_| rng.next() as u8)
                .collect(),
            1 => {
                let mut v = REQ_MAGIC.to_vec();
                v.extend((0..(rng.next() % 200) as usize).map(|_| rng.next() as u8));
                v
            }
            _ => {
                let mut v = seed_frame.clone();
                for _ in 0..1 + rng.next() % 4 {
                    let at = (rng.next() as usize) % v.len();
                    v[at] = rng.next() as u8;
                }
                v
            }
        };
        // Errors are the expected outcome; the property is "returns, never panics/aborts".
        if solve_frame(&frame, || true).is_ok() {
            accepted += 1;
        }
    }
    assert!(accepted < 20_000);
}
