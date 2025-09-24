// Adapted from https://github.com/astral-sh/uv/blob/main/docs/js/extra.js which
// is dual-licensed as MIT & Apache 2.0. See https://github.com/astral-sh/uv and
// https://docs.astral.sh/uv/.
//
// This code is very similar but two changes were made:
//
//   - The logic in cleanupClipboardText handles a different DOM structure
//   - Some of the code was reorganized and some comments were added

// Exclude "Generic Prompt" and "Generic Output" spans from copy
// Generic Prompt is "$ " and Gemeric Output is ">>> "
const excludedClasses = ["gp", "go"];

function cleanupClipboardText(targetSelector) {
  const targetElement = document.querySelector(targetSelector);

  // targetElement has one or more spans for each line and each line has one or
  // more spans for segments of a line. It's the segments of each line we want
  // to clean up
  //
  // <code>                  <-- code block
  //     <span>              <-- line in code block
  //         <span></span>   <-- segment in a line
  //         <span></span>   <-- segment in a line
  //         <span></span>   <-- segment in a line
  //     </span>
  //     <span>...</span>
  //     <span>...</span>
  // </code>

  return Array.from(targetElement.childNodes)   // <-- array of lines
    .map((span_el) =>
      Array.from(span_el.childNodes).filter(    // <-- array of segments
        (node) => !excludedClasses.some((excludedClass) =>
          node?.classList?.contains(excludedClass)
        )
      )
        .map((node) => node.textContent)
        .filter((s) => s != "").join("").trim()
    ).filter((s) => s !== "").join("\n").trim()
}

// Sets copy text to attributes lazily using an Intersection Observer.
function setCopyText() {
  // The `data-clipboard-text` attribute allows for customized content in the copy
  // See: https://www.npmjs.com/package/clipboard#copy-text-from-attribute
  const attr = "clipboardText";
  // all "copy" buttons whose target selector is a <code> element
  const elements = document.querySelectorAll(
    'button[data-clipboard-target$="code"]'
  );
  const observer = new IntersectionObserver((entries) => {
    entries.forEach((entry) => {
      // target in the viewport that have not been patched
      if (
        entry.intersectionRatio > 0 &&
        entry.target.dataset[attr] === undefined
      ) {
        entry.target.dataset[attr] = cleanupClipboardText(
          entry.target.dataset.clipboardTarget
        );
      }
    });
  });

  elements.forEach((elt) => {
    observer.observe(elt);
  });
}

// Using the document$ observable is particularly important if you are using instant loading since
// it will not result in a page refresh in the browser
// See `How to integrate with third-party JavaScript libraries` guideline:
// https://squidfunk.github.io/mkdocs-material/customization/?h=javascript#additional-javascript
document$.subscribe(function () {
  setCopyText();
});
