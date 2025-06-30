"use strict";

let originalContent = "";
const textarea = document.getElementById("textarea");
const menuBtn = document.getElementById("menu-btn");
const saveBtn = document.getElementById("save-btn");

menuBtn.addEventListener("click", toggleIndex);
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
});

function toggleIndex() {
  const indexlist = document.getElementById("indexlist");
  indexlist.classList.toggle("show");
}

async function fetchIndex() {
  const indexListEl = document.getElementById("indexlist");
  indexListEl.innerHTML = ""; // Clear existing list
  const ul = document.createElement("ul");
  try {
    const json = await doFetch("/getindex");
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
        await loadFile(file.fileName);
        toggleIndex(); // Hide index after selection
      });
      ul.appendChild(li);
    }
    indexListEl.appendChild(ul);
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
