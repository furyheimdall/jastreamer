export const SUPPORTED_PROTOCOL_MAJORS = [3, 2] as const;
export const SUPPORTED_MAJORS_HEADER = "X-Jake-Supported-Protocol-Majors";
export const SELECTED_MAJOR_HEADER = "X-Jake-Selected-Protocol-Major";
export const NO_COMMON_MAJOR_STATUS = 426;

export type MajorSelection =
  | { readonly kind: "selected"; readonly major: number }
  | { readonly kind: "upgrade_required"; readonly httpStatus: 426 };

export const selectProtocolMajor = (
  local: readonly number[],
  remote: readonly number[],
): MajorSelection => {
  const major = SUPPORTED_PROTOCOL_MAJORS.find(
    (candidate) => local.includes(candidate) && remote.includes(candidate),
  );
  return major === undefined
    ? { kind: "upgrade_required", httpStatus: NO_COMMON_MAJOR_STATUS }
    : { kind: "selected", major };
};
