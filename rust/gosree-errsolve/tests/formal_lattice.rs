//! Formal semilattice, fixpoint quiescence, termination, and `GSE1`/`GSR1` frame
//! verification for `gosree-errsolve`.

use gosree_errsolve::{
    decode_package, encode_result, solve_frame, solve_package, BlockIR, Finding, FuncIR, OpIR,
    PackageResult, PhiIR, SolveError, Terminator, TupleMeta, ASSIGN_NIL_MUST_NIL,
    ASSIGN_NIL_NON_NIL, COND_IS_NIL, COND_OPAQUE, FUNC_RETURNS_CONCRETE_ERR_PTR, NONE_U32,
    REQ_MAGIC, RESP_MAGIC, RULE_ERR002, RULE_ERR004, SLOT_IS_ERROR, SUB_ERR002_UNCHECKED,
    SUB_ERR004_MAY_NIL,
};

/// Proves fixpoint soundness across asymmetric diamond Phi merges (`bb0 -> {bb2, bb1}`,
/// `bb1 -> bb2 -> bb3`) where `bb2` is popped before `bb1`, and `bb1`'s outgoing state
/// matches `in_states[2]` (already seeded by `bb0`) while newly activating `bb2`'s second
/// `PhiIR` arm (`s1 = MustNil` / `Origin::Single(0)`).
///
/// Pre-fix bug: gating worklist re-enqueue solely on `in_states[succ].join_from(edge_st)`
/// skipped re-running `bb2` when `out_edges[1]` transitioned from `unreachable` to
/// `reachable` with the same ambient slot values as `in_states[2]`, leaving `bb3` with
/// a stale `NonNil` / `Safe` state and causing false negatives in Phase 1
/// (`may_return_nil == false`), `ERR004`, and `ERR002`.
#[test]
fn diamond_phi_late_predecessor_propagates_to_downstream_err004_and_err002() {
    // 1. Concrete error pointer diamond (`ERR004` + `may_return_nil`):
    //    slot 0: NonNil, slot 1: MustNil, slot 2: Phi(bb0: s0, bb1: s1) -> MayNil.
    let err004_fn = FuncIR {
        flags: FUNC_RETURNS_CONCRETE_ERR_PTR,
        slot_flags: vec![0, 0, 0],
        tuples: vec![],
        blocks: vec![
            // bb0: s0 = NonNil; s1 = MustNil; branch true->bb2 (popped first!), false->bb1
            BlockIR {
                phis: vec![],
                ops: vec![
                    OpIR::AssignSlot {
                        site_id: 1,
                        dst_slot: 0,
                        src_slot: NONE_U32,
                        nil_hint: ASSIGN_NIL_NON_NIL,
                        writable_hint: 0,
                    },
                    OpIR::AssignSlot {
                        site_id: 2,
                        dst_slot: 1,
                        src_slot: NONE_U32,
                        nil_hint: ASSIGN_NIL_MUST_NIL,
                        writable_hint: 0,
                    },
                ],
                term: Terminator::Cond {
                    true_bb: 2,
                    false_bb: 1,
                    cond_kind: COND_OPAQUE,
                    cond_slot: NONE_U32,
                },
            },
            // bb1: pass-through Jump -> bb2 (carries identical ambient slots to bb0!)
            BlockIR {
                phis: vec![],
                ops: vec![],
                term: Terminator::Jump { target: 2 },
            },
            // bb2: s2 = phi(bb0: s0 [NonNil], bb1: s1 [MustNil]) -> Jump bb3
            BlockIR {
                phis: vec![PhiIR {
                    dst_slot: 2,
                    incoming: vec![(0, 0), (1, 1)],
                }],
                ops: vec![],
                term: Terminator::Jump { target: 3 },
            },
            // bb3: boxes s2 into `error` interface and returns s2 as concrete `*MyErr`
            BlockIR {
                phis: vec![],
                ops: vec![OpIR::BoxConcreteErr {
                    site_id: 40,
                    ptr_slot: 2,
                }],
                term: Terminator::Return {
                    site_id: 41,
                    ret_slot: 2,
                    flags: 0,
                },
            },
        ],
    };

    let res = solve_package(&[err004_fn], &mut || true).expect("solve err004 diamond");
    assert_eq!(
        res.may_return_nil,
        vec![true],
        "Phase 1 must prove concrete-nil reachability across late Phi predecessor"
    );
    assert!(
        res.findings.contains(&Finding {
            func_idx: 0,
            site_id: 40,
            rule_code: RULE_ERR004,
            sub_kind: SUB_ERR004_MAY_NIL,
        }),
        "Phase 2 must report ERR004 downstream of diamond Phi, got {:?}",
        res.findings
    );

    // 2. Unchecked companion value diamond (`ERR002`):
    //    slot 0: Safe companion, slot 1: Unchecked Single(0) companion, slot 2: err, slot 3: Phi(bb0: s0, bb1: s1)
    let err002_fn = FuncIR {
        flags: 0,
        slot_flags: vec![0, 0, SLOT_IS_ERROR, 0],
        tuples: vec![TupleMeta {
            err_slot: 2,
            call_site_id: 10,
            flags: 0,
        }],
        blocks: vec![
            BlockIR {
                phis: vec![],
                ops: vec![
                    OpIR::BindTuple {
                        site_id: 10,
                        tuple_id: 0,
                        err_slot: 2,
                        companions: vec![1],
                    },
                    OpIR::ObserveErr {
                        site_id: 11,
                        slot: 2,
                    },
                ],
                term: Terminator::Cond {
                    true_bb: 2,
                    false_bb: 1,
                    cond_kind: COND_OPAQUE,
                    cond_slot: NONE_U32,
                },
            },
            BlockIR {
                phis: vec![],
                ops: vec![],
                term: Terminator::Jump { target: 2 },
            },
            BlockIR {
                phis: vec![PhiIR {
                    dst_slot: 3,
                    incoming: vec![(0, 0), (1, 1)],
                }],
                ops: vec![],
                term: Terminator::Jump { target: 3 },
            },
            BlockIR {
                phis: vec![],
                ops: vec![OpIR::UseCompanion {
                    site_id: 77,
                    slot: 3,
                }],
                term: Terminator::Stop,
            },
        ],
    };

    let res2 = solve_package(&[err002_fn], &mut || true).expect("solve err002 diamond");
    assert!(
        res2.findings.contains(&Finding {
            func_idx: 0,
            site_id: 77,
            rule_code: RULE_ERR002,
            sub_kind: SUB_ERR002_UNCHECKED,
        }),
        "Phase 2 must report ERR002 downstream of diamond Phi, got {:?}",
        res2.findings
    );
}

/// Proves cyclic loop convergence when an `err == nil` promotion (`COND_IS_NIL`) on
/// iteration 1 is invalidated on iteration 2 by a back-edge reassigning `err` to an
/// unrelated error (`ErrLink::Tuple(0) -> ErrLink::None`), ensuring `out_edges` never
/// oscillates and converges well inside the iteration budget.
#[test]
fn cyclic_err_link_invalidation_converges_monotonically_without_oscillation() {
    // bb0: BindTuple(tuple 0 -> err s0, comp s1); Jump bb1
    // bb1: Cond(COND_IS_NIL s0) -> true: bb2, false: bb3
    // bb2: s2 = Phi(bb1: s1); AssignSlot(s0 = NonNil [clears ErrLink to None!]); Cond -> true: bb1 (back-edge!), false: bb4
    // bb3: Stop
    // bb4: UseCompanion(s2); Stop
    let loop_fn = FuncIR {
        flags: 0,
        slot_flags: vec![SLOT_IS_ERROR, 0, 0],
        tuples: vec![TupleMeta {
            err_slot: 0,
            call_site_id: 1,
            flags: 0,
        }],
        blocks: vec![
            BlockIR {
                phis: vec![],
                ops: vec![OpIR::BindTuple {
                    site_id: 1,
                    tuple_id: 0,
                    err_slot: 0,
                    companions: vec![1],
                }],
                term: Terminator::Jump { target: 1 },
            },
            BlockIR {
                phis: vec![],
                ops: vec![],
                term: Terminator::Cond {
                    true_bb: 2,
                    false_bb: 3,
                    cond_kind: COND_IS_NIL,
                    cond_slot: 0,
                },
            },
            BlockIR {
                phis: vec![PhiIR {
                    dst_slot: 2,
                    incoming: vec![(1, 1)],
                }],
                ops: vec![OpIR::AssignSlot {
                    site_id: 2,
                    dst_slot: 0,
                    src_slot: NONE_U32,
                    nil_hint: ASSIGN_NIL_MUST_NIL, // err = nil (unlinked from tuple 0!)
                    writable_hint: 0,
                }],
                term: Terminator::Cond {
                    true_bb: 1,
                    false_bb: 4,
                    cond_kind: COND_OPAQUE,
                    cond_slot: NONE_U32,
                },
            },
            BlockIR {
                phis: vec![],
                ops: vec![],
                term: Terminator::Stop,
            },
            BlockIR {
                phis: vec![],
                ops: vec![OpIR::UseCompanion {
                    site_id: 99,
                    slot: 2,
                }],
                term: Terminator::Stop,
            },
        ],
    };

    let res = solve_package(&[loop_fn], &mut || true).expect("loop must converge");
    // On iteration 1, `s0` is `ErrLink::Tuple(0)`, so `s0 == nil` promotes `s1` to `Safe`.
    // On iteration 2 via back-edge `bb2 -> bb1`, `s0` was reassigned (`ErrLink::None`),
    // so `s0 == nil` no longer proves `s1` (from an unchecked path if any) was checked —
    // monotone `out_edges` join (`Safe ⊔ Single(0) = UncheckedMix(0)`) converges in 2 passes!
    assert!(
        res.findings.contains(&Finding {
            func_idx: 0,
            site_id: 99,
            rule_code: RULE_ERR002,
            sub_kind: SUB_ERR002_UNCHECKED,
        }),
        "back-edge unlinked err = nil must conservatively flag ERR002 rather than oscillating or trusting unlinked nil: {:?}",
        res.findings
    );
}

/// Verifies `GSE1` / `GSR1` binary frame boundary invariants: trailing-byte rejection,
/// truncated-header rejection, magic-byte enforcement, and deterministic `GSR1` encoding.
#[test]
fn gse1_gsr1_binary_protocol_enforces_exact_framing_and_determinism() {
    let mut valid_empty = Vec::new();
    valid_empty.extend_from_slice(&REQ_MAGIC);
    valid_empty.extend_from_slice(&0u32.to_le_bytes());
    let funcs = decode_package(&valid_empty).expect("valid empty GSE1");
    assert!(funcs.is_empty());

    let out = solve_frame(&valid_empty, || true).expect("solve empty GSE1");
    assert_eq!(&out[0..4], &RESP_MAGIC);
    assert_eq!(out.len(), 12); // magic(4) + num_funcs(4) + num_findings(4)

    // Disproof 1: Trailing byte MUST fail closed with `TrailingBytes(1)`.
    let mut with_trailing = valid_empty.clone();
    with_trailing.push(0x00);
    assert_eq!(
        decode_package(&with_trailing).unwrap_err(),
        SolveError::TrailingBytes(1)
    );

    // Disproof 2: Truncated header MUST fail closed with `Truncated`.
    assert_eq!(
        decode_package(&valid_empty[..7]).unwrap_err(),
        SolveError::Truncated
    );

    // Disproof 3: Corrupted magic MUST fail closed with `BadMagic`.
    let mut bad_magic = valid_empty;
    bad_magic[3] = b'2';
    assert_eq!(
        decode_package(&bad_magic).unwrap_err(),
        SolveError::BadMagic
    );

    // Deterministic `GSR1` encoding check
    let pkg_res = PackageResult {
        may_return_nil: vec![true, false],
        findings: vec![Finding {
            func_idx: 0,
            site_id: 42,
            rule_code: RULE_ERR004,
            sub_kind: SUB_ERR004_MAY_NIL,
        }],
    };
    let enc1 = encode_result(&pkg_res);
    let enc2 = encode_result(&pkg_res);
    assert_eq!(enc1, enc2);
    assert_eq!(&enc1[0..4], &RESP_MAGIC);
}
