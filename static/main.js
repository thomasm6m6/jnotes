"use strict";

let originalContent = "";
let textarea, menuBtn, saveBtn, header;
let currentNoteInfo = {
  fileName: null,
  attachmentCount: 0,
  currentIndex: -1
};

window.addEventListener("load", async function () {
  textarea = document.getElementById("textarea");
  menuBtn = document.getElementById("menu-btn");
  saveBtn = document.getElementById("save-btn");
  header = document.getElementById("header");

  menuBtn.addEventListener("click", showIndex);
  saveBtn.addEventListener("click", submitFile);

  textarea.addEventListener("input", () => {
    if (textarea.value !== originalContent) {
      saveBtn.classList.remove("disabled");
    } else {
      saveBtn.classList.add("disabled");
    }
  });

  textarea.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault();
      if (textarea.value !== originalContent) { // Only save if there are changes
        submitFile();
      }
    }
  });

  // Initial setup, and then router takes over.
  window.addEventListener("beforeunload", (e) => {
    if (textarea.value !== originalContent) {
      const data = JSON.stringify({ text: textarea.value });
      navigator.sendBeacon("/save", new Blob([data], { type: 'application/json' }));
    }
  });

  const searchInput = document.getElementById("search-input");
  let debounceTimer;
  searchInput.addEventListener("input", (e) => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      const query = e.target.value;
      const url = query ? `/#index?q=${encodeURIComponent(query)}` : '/#index';
      history.replaceState({ page: 'index', query: query }, '', url);
      router();
    }, 300);
  });

  await router();

  const viewer = document.getElementById("image-viewer");
  viewer.addEventListener("click", (e) => {
    // Close if the background or the close button is clicked.
    if (e.target === viewer || e.target.classList.contains('close-viewer')) {
      viewer.style.display = "none";
      // Clear src to stop loading if in progress
      viewer.querySelector(".viewer-content").src = "";
    }
  });

  window.addEventListener("keydown", (e) => {
    const viewer = document.getElementById("image-viewer");
    if (viewer.style.display !== "flex") {
      return;
    }

    if (e.key === "ArrowUp" || e.key === "ArrowDown") {
      e.preventDefault();
      if (currentNoteInfo.attachmentCount <= 1) return;

      if (e.key === "ArrowDown") {
        currentNoteInfo.currentIndex = (currentNoteInfo.currentIndex + 1) % currentNoteInfo.attachmentCount;
      } else { // ArrowUp
        currentNoteInfo.currentIndex = (currentNoteInfo.currentIndex - 1 + currentNoteInfo.attachmentCount) % currentNoteInfo.attachmentCount;
      }

      const viewerImg = viewer.querySelector(".viewer-content");
      viewerImg.src = `/getattachment?note=${currentNoteInfo.fileName}&index=${currentNoteInfo.currentIndex}`;
    }
  });
});

window.addEventListener("popstate", router);

function showIndex() {
  if (location.hash.startsWith('#index')) return;
  history.pushState({ page: 'index' }, "", "#index");
  router();
}

function hideIndex() {
  const indexlist = document.getElementById("indexlist");
  indexlist.classList.remove("show");
}

async function router() {
  const params = new URLSearchParams(location.search);
  const note = params.get('note');

  if (location.hash.startsWith('#index')) {
    document.getElementById("indexlist").classList.add("show");
    const hashParams = new URLSearchParams(location.hash.substring(location.hash.indexOf('?') + 1));
    const query = hashParams.get('q') || '';
    const searchInput = document.getElementById("search-input");
    if (document.activeElement !== searchInput) {
        searchInput.value = query;
    }
    await fetchIndex(query);
    if (history.state && history.state.scrollPosition) {
        document.getElementById('indexlist').scrollTop = history.state.scrollPosition;
    }
  } else {
    hideIndex();
    await loadFile(note);
  }
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

      if (file.attachmentCount > 0) {
        const attachmentSpan = document.createElement("span");
        attachmentSpan.className = "index-entry-attachments";
        attachmentSpan.textContent = `${file.attachmentCount} 📎`;
        titleSpan.appendChild(attachmentSpan);
      }

      const previewSpan = document.createElement("small");
      previewSpan.className = "index-entry-preview";
      previewSpan.textContent = file.preview;

      li.appendChild(titleSpan);
      li.appendChild(previewSpan);

      li.addEventListener("click", async () => {
        if (textarea.value !== originalContent) {
          await submitFile();
        }
        const indexList = document.getElementById('indexlist');
        const currentState = history.state || {};
        history.replaceState({ ...currentState, scrollPosition: indexList.scrollTop }, '', window.location.href);

        const noteUrl = `/?note=${file.fileName}`;
        history.pushState({ page: 'note', fileName: file.fileName }, '', noteUrl);
        router();
      });
      ul.appendChild(li);
    }
    resultsContainer.appendChild(ul);
  } catch (error) {
    console.error(`error fetching index: ${error.message}`);
  }
}

async function loadFile(fileName) {
  const attachmentGutter = document.getElementById("attachment-gutter");
  attachmentGutter.innerHTML = "";
  currentNoteInfo = { fileName: null, attachmentCount: 0, currentIndex: -1 };

  try {
    const url = fileName ? `/getfile?name=${fileName}` : "/getfile";
    const json = await doFetch(url);
    currentNoteInfo.fileName = json.fileName;
    currentNoteInfo.attachmentCount = json.attachmentCount;
    const date = parseDate(json.fileName);
    header.innerText = formatDate(date);
    textarea.value = json.content;
    originalContent = json.content;
    saveBtn.classList.add("disabled");

    if (json.attachmentCount > 0) {
      for (let i = 0; i < json.attachmentCount; i++) {
        const thumb = document.createElement("img");
        thumb.src = `/db/${json.fileName}/thumbnails/${i}.png`;
        thumb.addEventListener("click", () => {
          currentNoteInfo.currentIndex = i;
          const viewer = document.getElementById("image-viewer");
          const viewerImg = viewer.querySelector(".viewer-content");
          viewerImg.src = `/getattachment?note=${currentNoteInfo.fileName}&index=${currentNoteInfo.currentIndex}`;
          viewer.style.display = "flex";
        });
        attachmentGutter.appendChild(thumb);
      }
    }
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
