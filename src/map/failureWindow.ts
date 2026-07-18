export type FailureWindow = {
  failures: number[];
  sustained: boolean;
};

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
