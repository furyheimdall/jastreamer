const requireActionable = async (locator, code) => {
  try {
    await locator.click({ trial: true, timeout: 10_000 });
  } catch (cause) {
    throw new Error(code, { cause });
  }
};

export const replaceTask19FlutterText = async (page, locator, value) => {
  await requireActionable(locator, "TASK19_FLUTTER_TEXTBOX_NOT_ACTIONABLE"); await locator.click(); await locator.focus();
  if (!await locator.evaluate((element) => element === document.activeElement)) throw new Error("TASK19_FLUTTER_TEXTBOX_FOCUS_FAILED");
  const prior = await locator.inputValue(); if (prior !== "") { await page.keyboard.press("Control+A"); await page.keyboard.press("Backspace"); }
  if (value !== "") await locator.pressSequentially(value);
  const observed = await locator.inputValue(); if (observed !== value) throw new Error("TASK19_FLUTTER_TEXT_CONTROLLER_NOT_UPDATED");
  return locator.evaluate((element) => ({ role: element.getAttribute("role"), value: element.value, disabled: element.getAttribute("aria-disabled"), focused: element === document.activeElement }));
};

export const activateTask19Flutter = async (page, locator, gesture = "keyboard") => {
  await requireActionable(locator, "TASK19_FLUTTER_ACTION_NOT_ACTIONABLE"); await locator.focus();
  if (!await locator.evaluate((element) => element === document.activeElement)) throw new Error("TASK19_FLUTTER_ACTION_FOCUS_FAILED");
  if (gesture === "keyboard") {
    await page.keyboard.press("Enter");
  } else if (gesture === "semantic-click") {
    await locator.click();
  } else if (gesture === "pointer-click") {
    const box = await locator.boundingBox();
    if (box === null) throw new Error("TASK19_FLUTTER_ACTION_NOT_ACTIONABLE");
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.evaluate(() => new Promise(requestAnimationFrame));
    await page.mouse.up();
  } else {
    throw new Error("TASK19_FLUTTER_GESTURE_INVALID");
  }
};
