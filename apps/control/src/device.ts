const userAgent = navigator.userAgent;

// Layout hints only: a narrow window or an iPad's desktop-style UA is not a phone.
export const isPhone = !/\biPad\b/i.test(userAgent) && (
  /\biPhone\b|\biPod\b/i.test(userAgent)
  || (/\bAndroid\b/i.test(userAgent) && /\bMobile\b/i.test(userAgent))
);

export const isAppleMobile = /\biPhone\b|\biPad\b|\biPod\b/i.test(userAgent)
  || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
