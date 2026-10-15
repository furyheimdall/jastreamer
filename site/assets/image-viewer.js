(() => {
  const viewer = document.getElementById("image-viewer");
  if (!viewer || typeof viewer.showModal !== "function") return;

  const image = viewer.querySelector(".image-viewer-image");
  const stage = viewer.querySelector(".image-viewer-stage");
  const root = document.documentElement;
  let origin;
  let scrollX = 0;
  let scrollY = 0;

  document.querySelectorAll("a[data-image-viewer]").forEach((link) => {
    link.setAttribute("aria-haspopup", "dialog");
    link.setAttribute("aria-controls", viewer.id);
    link.addEventListener("click", (event) => {
      if (event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      event.preventDefault();

      const thumbnail = link.querySelector("img");
      origin = link;
      scrollX = window.scrollX;
      scrollY = window.scrollY;
      image.src = link.href;
      image.alt = thumbnail.alt;
      image.setAttribute("width", thumbnail.getAttribute("width"));
      image.setAttribute("height", thumbnail.getAttribute("height"));
      root.style.setProperty("--image-viewer-scroll-top", `${-scrollY}px`);
      root.style.setProperty("--image-viewer-scroll-left", `${-scrollX}px`);
      root.classList.add("image-viewer-open");
      viewer.showModal();
      stage.scrollTop = 0;
      stage.scrollLeft = 0;
    });
  });

  // The image itself stays open; every surrounding surface returns to the page.
  viewer.addEventListener("click", (event) => {
    if (event.target !== image) viewer.close();
  });

  // Native Escape and the Close button use the same restoration path.
  viewer.addEventListener("close", () => {
    root.classList.remove("image-viewer-open");
    root.style.removeProperty("--image-viewer-scroll-top");
    root.style.removeProperty("--image-viewer-scroll-left");
    origin.focus({ preventScroll: true });
    window.scrollTo({ left: scrollX, top: scrollY, behavior: "instant" });
  });
})();
