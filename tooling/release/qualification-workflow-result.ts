export type SchedulerJobState = "queued" | "in_progress" | "success" | "failure" | "cancelled" | "skipped";
export type QualificationJobReduction = Readonly<{ status: "pending" | "satisfied" | "unsatisfied"; authoritative: true; retryDispatches: 0 }>;
export const reduceQualificationJobs = (jobs: Readonly<{ server: SchedulerJobState; control: SchedulerJobState }>): QualificationJobReduction => {
  const states = [jobs.server, jobs.control];
  if (states.some((state) => state === "queued" || state === "in_progress")) return { status: "pending", authoritative: true, retryDispatches: 0 };
  return states.every((state) => state === "success") ? { status: "satisfied", authoritative: true, retryDispatches: 0 } : { status: "unsatisfied", authoritative: true, retryDispatches: 0 };
};
