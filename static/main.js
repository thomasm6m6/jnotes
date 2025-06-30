"use strict";

let originalContent = "";
const textarea = document.getElementById("textarea");
const menuBtn = document.getElementById("menu-btn");
const saveBtn = document.getElementById("save-btn");

menuBtn.addEventListener("click", showIndex);
saveBtn.addEventListener("click", submitFile);

textarea.addEventListener("input", () => {
  if (textarea.value !== originalContent) {
    saveBtn.classList.remove("disabled");
  } else {
    saveBtn.classList.add("disabled");
  }
});

window.addEventListener("load", async function () {
  try {
    await fetchIndex(); // Fetch and render the index
    await loadFile(); // Load the default file
  } catch (error) {
    console.error(`error loading file: ${error.message}`);
  }

  window.addEventListener("popstate", () => {
    hideIndex();
  });

  if (location.hash === "#index") {
    document.getElementById("indexlist").classList.add("show");
  }

  window.addEventListener("beforeunload", (e) => {
    if (textarea.value !== originalContent) {
      // Use sendBeacon for reliability on page unload
      const data = JSON.stringify({ text: textarea.value });
      navigator.sendBeacon("/save", new Blob([data], { type: 'application/json' }));
    }
  });

  const searchInput = document.getElementById("search-input");
  let debounceTimer;
  searchInput.addEventListener("input", (e) => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      fetchIndex(e.target.value);
    }, 300);
  });
});

function showIndex() {
  const indexlist = document.getElementById("indexlist");
  if (!indexlist.classList.contains("show")) {
    indexlist.classList.add("show");
    history.pushState({ indexVisible: true }, "", "#index");
  }
}

function hideIndex() {
  const indexlist = document.getElementById("indexlist");
  indexlist.classList.remove("show");
}

async function fetchIndex(query = "") {
  const resultsContainer = document.getElementById("results-container");
  resultsContainer.innerHTML = ""; // Clear existing list
  const ul = document.createElement("ul");
  try {
    const url = query ? `/getindex?q=${encodeURIComponent(query)}` : "/getindex";
    const json = await doFetch(url);
    for (const file of json.files) {
      const li = document.createElement("li");

      const date = parseDate(file.fileName);
      const formattedDate = formatDate(date);

      const titleSpan = document.createElement("span");
      titleSpan.className = "index-entry-title";
      titleSpan.textContent = formattedDate;

      const previewSpan = document.createElement("small");
      previewSpan.className = "index-entry-preview";
      previewSpan.textContent = file.preview;

      li.appendChild(titleSpan);
      li.appendChild(previewSpan);

      li.addEventListener("click", async () => {
        if (textarea.value !== originalContent) {
          await submitFile();
        }
        await loadFile(file.fileName);
        history.back();
      });
      ul.appendChild(li);
    }
    resultsContainer.appendChild(ul);
  } catch (error) {
    console.error(`error fetching index: ${error.message}`);
  }
}

async function loadFile(fileName) {
  try {
    const url = fileName ? `/getfile?name=${fileName}` : "/getfile";
    const json = await doFetch(url);
    const date = parseDate(json.fileName);
    header.innerText = formatDate(date);
    textarea.value = json.content;
    originalContent = json.content;
    saveBtn.classList.add("disabled");
  } catch (error) {
    console.error(`error loading file: ${error.message}`);
  }
}

async function submitFile() {
  saveBtn.classList.add("disabled");
  const saveIcon = saveBtn.querySelector("i");
  saveIcon.classList.remove("fa-check");
  saveIcon.classList.add("fa-spinner", "spinner-anim");

  const text = textarea.value;
  try {
    const json = await doFetch("/save", {
      method: "POST",
      body: JSON.stringify({ text }),
    });
    if (json.status !== "save scheduled") {
      throw new Error(`Save failed, unexpected status: ${json.status}`);
    }
    originalContent = text;
  } catch (error) {
    console.error(error.message);
    // Re-enable save button on failure to allow retry.
    saveBtn.classList.remove("disabled");
  } finally {
    saveIcon.classList.remove("fa-spinner", "spinner-anim");
    saveIcon.classList.add("fa-check");
  }
}

async function doFetch(resource, options) {
  const response = await fetch(resource, options);
  if (!response.ok) {
    throw new Error(`Response status: ${response.status}`);
  }

  return response.json();
}

function parseDate(dateString) {
  const year = parseInt(dateString.slice(0, 4), 10);
  const month = parseInt(dateString.slice(4, 6), 10) - 1;
  const day = parseInt(dateString.slice(6, 8), 10);
  return new Date(year, month, day);
}

function formatDate(date) {
  return date.toLocaleDateString(undefined, {
    year: "numeric",
    month: "long",
    day: "numeric",
  });
}
