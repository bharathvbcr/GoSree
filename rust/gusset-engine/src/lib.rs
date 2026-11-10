//! Gusset umbrella staticlib (`libgusset.a`) for GoSree (`gosree`).
//!
//! Per Gusset R14, a Go binary links exactly one Rust staticlib (`libgusset.a`),
//! which owns the `Counting<System>` global allocator, installs the global
//! error-discipline CFG solver on opcode 0 (`set_engine_handler`), and registers
//! the intentional panic self-test opcode (`OPCODE_SELF_TEST_PANIC`) so Go can
//! prove the `catch_unwind` panic firewall against this archive.

use std::sync::OnceLock;

use gusset_core::{register_engine, set_engine_handler, Counting, JobContext};

/// Tracks every Rust heap allocation in the host Go process so `gusset.Stats()`
/// and `gusset.AdviseMemoryLimit()` reflect actual Rust memory usage.
#[global_allocator]
static ALLOCATOR: Counting<std::alloc::System> = Counting::new(std::alloc::System);

/// Intentional panic opcode used exclusively by `gusseterr.SelfTest` on a
/// throwaway `gusset.Handle` to verify `ErrPanic` -> `ErrPoisoned` -> clean
/// handle recovery.
pub const OPCODE_SELF_TEST_PANIC: u32 = 0x7fff_0001;

static INIT_STATUS: OnceLock<i32> = OnceLock::new();

/// Installs the GoSree error-discipline solver on Gusset's global handler
/// (opcode 0) and registers `OPCODE_SELF_TEST_PANIC`.
///
/// Idempotent: safe if invoked more than once in the same process.
/// Returns `0` when all opcodes registered cleanly, or the count of failed
/// registrations otherwise.
///
/// # Safety
///
/// Called from Go via cgo (`#cgo noescape`, `#cgo nocallback`) before the first
/// `gusset.Open` submission.
#[no_mangle]
pub unsafe extern "C" fn gosree_gusset_init() -> i32 {
    *INIT_STATUS.get_or_init(|| {
        set_engine_handler(|ctx: &JobContext, input: &[u8]| -> Result<Vec<u8>, String> {
            ctx.check()
                .map_err(|reason| format!("cancelled: {reason:?}"))?;
            gosree_errsolve::solve_frame(input, || ctx.check().is_ok())
                .map_err(|err| err.to_string())
        });

        let results = [register_engine(
            OPCODE_SELF_TEST_PANIC,
            |_: &JobContext, _: &[u8]| -> Result<Vec<u8>, String> {
                panic!("gosree gusset self-test panic");
            },
        )];
        results.iter().filter(|r| r.is_err()).count() as i32
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn init_is_idempotent_and_succeeds() {
        let rc1 = unsafe { gosree_gusset_init() };
        let rc2 = unsafe { gosree_gusset_init() };
        assert_eq!(rc1, 0);
        assert_eq!(rc2, 0);
    }

    #[test]
    fn solve_roundtrip_from_umbrella() {
        let frame = gosree_errsolve::encode_package(&[]);
        let resp = gosree_errsolve::solve_frame(&frame, || true).expect("empty solve");
        assert_eq!(&resp[0..4], b"GSR1");
    }
}
