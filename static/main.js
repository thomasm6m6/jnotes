"use strict";

const textarea = document.getElementById("textarea");
const menuBtn = document.getElementById("menu-btn");
const saveBtn = document.getElementById("save-btn");

menuBtn.addEventListener("click", toggleIndex);
saveBtn.addEventListener("click", submitFile);

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
    for (const fileName of json.fileNames) {
      const li = document.createElement("li");
      li.textContent = fileName;
      li.addEventListener("click", async () => {
        await loadFile(fileName);
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
  } catch (error) {
    console.error(`error loading file: ${error.message}`);
  }
}

async function submitFile() {
  const text = textarea.value;
  try {
    const json = await doFetch("/save", {
      method: "POST",
      body: JSON.stringify({ text }),
    });
  } catch (error) {
    console.error(error.message);
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
