// An onComplete that throws fails the run, but the launcher sends the error only to its own log, so
// the run's output would not say why (#435): print it, then rethrow.
export async function printFailure(complete: () => unknown) {
  try {
    await complete();
  } catch (e) {
    console.error(String(e));
    throw e;
  }
}
