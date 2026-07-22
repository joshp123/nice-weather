export type FailureWindow = {
  failures: number[];
  sustained: boolean;
};

export type RendererFailureReason = 'init' | 'context' | 'timeout' | 'base-map';
export type RendererRecoveryState = Readonly<{
  activeAttempt: number;
  retryUsed: boolean;
  retryPending: boolean;
}>;

export function activateRendererAttempt(previous: RendererRecoveryState, activeAttempt: number): RendererRecoveryState {
  return previous.activeAttempt === activeAttempt ? previous : { ...previous, activeAttempt, retryPending: false };
}

export function decideRendererFailure(previous: RendererRecoveryState, failedAttempt: number, reason: RendererFailureReason): { state: RendererRecoveryState; action: 'ignore' | 'retry' | 'fallback' } {
  if (failedAttempt !== previous.activeAttempt || previous.retryPending) return { state: previous, action: 'ignore' };
  if ((reason === 'init' || reason === 'context') && !previous.retryUsed) {
    return { state: { ...previous, retryUsed: true, retryPending: true }, action: 'retry' };
  }
  return { state: previous, action: 'fallback' };
}

export function withCurrentRenderer<T>(current: T | null, expected: T, operation: (renderer: T) => void): boolean {
  if (current !== expected) return false;
  operation(expected);
  return true;
}

export function detachRenderer<T>(current: T | null, failed: T): T | null {
  return current === failed ? null : current;
}

export function recordFailure(
  previous: readonly number[],
  now: number,
  policy = { windowMs: 5_000, minimumFailures: 5, minimumSpanMs: 1_500 },
): FailureWindow {
  const failures = previous.filter((failure) => failure >= now - policy.windowMs);
  failures.push(now);
  return {
    failures,
    sustained: failures.length >= policy.minimumFailures && now - failures[0] >= policy.minimumSpanMs,
  };
}
