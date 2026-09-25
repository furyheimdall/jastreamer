const userAgent = navigator.userAgent;

// Layout hints only: a narrow window or an iPad's desktop-style UA is not a phone.
export const isPhone = !/\biPad\b/i.test(userAgent) && (
  /\biPhone\b|\biPod\b/i.test(userAgent)
  || (/\bAndroid\b/i.test(userAgent) && /\bMobile\b/i.test(userAgent))
);

// Presentation only; native capabilities and authentication use their own checks.
export const embeddedClient = /\bJaStreamerAndroid\//i.test(userAgent) ? "android"
  : /\bJaStreamerDesktop\//i.test(userAgent) ? "desktop"
  : /\bJaStreamerIOS\//i.test(userAgent) ? "ios" : null;

export const isAppleMobile = /\biPhone\b|\biPad\b|\biPod\b/i.test(userAgent)
  || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

export function localOutputName(nativeAndroid: boolean, windowsDesktop = false): string {
  if (nativeAndroid) return "Android · jastreamer";
  return windowsDesktop ? "Windows · jastreamer" : browserOutputName();
}

// Browser-provided hints, not the operating system's private hostname.
export function browserOutputName(): string {
  const platform = /\biPhone\b|\biPod\b/i.test(userAgent) ? "iPhone"
    : isAppleMobile ? "iPad"
    : /\bAndroid\b/i.test(userAgent) ? "Android"
    : /\bWindows\b/i.test(userAgent) ? "Windows"
    : /\bCrOS\b/i.test(userAgent) ? "ChromeOS"
    : /\bMacintosh\b|\bMac OS X\b/i.test(userAgent) ? "Mac"
    : /\bLinux\b/i.test(userAgent) ? "Linux" : "";
  const browser = /\bEdg(?:e|A|iOS)?\//i.test(userAgent) ? "Edge"
    : /\bOPR\/|\bOpera\b/i.test(userAgent) ? "Opera"
    : /\bSamsungBrowser\//i.test(userAgent) ? "Samsung Internet"
    : /\bFirefox\/|\bFxiOS\//i.test(userAgent) ? "Firefox"
    : /\bChrome\/|\bHeadlessChrome\/|\bCriOS\//i.test(userAgent) ? "Chrome"
    : /\bSafari\//i.test(userAgent) ? "Safari" : "Web browser";
  return platform ? `${platform} · ${browser}` : browser;
}
