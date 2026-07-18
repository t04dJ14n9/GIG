import { renderAST, renderDiagnostics, renderSSAPlaceholder, renderTokens, renderTypes } from "/renderers.js";

const STORAGE_KEY = "ssa-study-progress-v1";
const stages = ["tokens", "ast", "types", "ssa"];
const state = {
  lessons: [],
  lesson: null,
  freeLab: false,
  stage: "tokens",
  result: null,
  selectedRange: null,
  selectedAST: -1,
  requestSequence: 0,
  timer: 0,
  progress: loadProgress(),
};

const ui = {
  lessonList: document.getElementById("lesson-list"),
  progressCount: document.getElementById("progress-count"),
  freeLab: document.getElementById("free-lab"),
  kicker: document.getElementById("lesson-kicker"),
  title: document.getElementById("lesson-title"),
  explanation: document.getElementById("lesson-explanation"),
  editor: document.getElementById("source-editor"),
  mirror: document.querySelector("#source-highlight code"),
  selection: document.getElementById("source-selection"),
  reset: document.getElementById("reset-source"),
  tabs: document.getElementById("stage-tabs"),
  output: document.getElementById("stage-output"),
  diagnostics: document.getElementById("diagnostics"),
  quiz: document.getElementById("quiz-panel"),
  status: document.getElementById("analysis-status"),
  traceStage: document.getElementById("trace-stage-label"),
};

boot();

async function boot() {
  bindEvents();
  try {
    const response = await fetch("/api/lessons");
    if (!response.ok) throw new Error(`lesson request returned ${response.status}`);
    state.lessons = await response.json();
    const first = state.lessons.find((lesson) => !state.progress.completed.includes(lesson.id)) || state.lessons[0];
    loadLesson(first.id);
  } catch (error) {
    setStatus(`Could not load lessons: ${error.message}`, "error");
    ui.output.replaceChildren(emptyMessage("The local lesson catalog did not load."));
  }
}

function bindEvents() {
  ui.editor.addEventListener("input", () => {
    state.selectedRange = null;
    state.selectedAST = -1;
    paintSource();
    scheduleAnalysis();
  });
  ui.editor.addEventListener("scroll", () => {
    const host = ui.mirror.parentElement;
    host.scrollTop = ui.editor.scrollTop;
    host.scrollLeft = ui.editor.scrollLeft;
  });
  ui.tabs.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-stage]");
    if (button) chooseStage(button.dataset.stage);
  });
  ui.reset.addEventListener("click", () => {
    if (state.lesson) setSource(state.lesson.source);
  });
  ui.freeLab.addEventListener("click", enterFreeLab);
}

function renderLessonRail() {
  ui.lessonList.replaceChildren();
  for (const lesson of state.lessons) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "lesson-button";
    if (!state.freeLab && state.lesson?.id === lesson.id) button.setAttribute("aria-current", "step");
    const number = span("lesson-number", String(lesson.number).padStart(2, "0"));
    const name = span("lesson-name", lesson.title);
    const done = span("lesson-check", state.progress.completed.includes(lesson.id) ? "✓" : "");
    done.setAttribute("aria-label", state.progress.completed.includes(lesson.id) ? "Complete" : "Not complete");
    button.append(number, name, done);
    button.addEventListener("click", () => loadLesson(lesson.id));
    ui.lessonList.append(button);
  }
  ui.progressCount.textContent = String(state.progress.completed.length);
  ui.freeLab.setAttribute("aria-pressed", state.freeLab ? "true" : "false");
}

function loadLesson(id) {
  const lesson = state.lessons.find((candidate) => candidate.id === id);
  if (!lesson) return;
  state.lesson = lesson;
  state.freeLab = false;
  state.stage = lesson.stage;
  ui.kicker.textContent = `Lesson ${lesson.number} / ${lesson.kicker}`;
  ui.title.textContent = lesson.title;
  ui.explanation.textContent = lesson.explanation;
  ui.freeLab.setAttribute("aria-pressed", "false");
  setSource(lesson.source);
  renderLessonRail();
  renderQuizPlaceholder();
  chooseStage(lesson.stage);
}

function enterFreeLab() {
  state.freeLab = true;
  ui.kicker.textContent = "Free lab / arbitrary single file";
  ui.title.textContent = "Ask the pipeline a concrete question";
  ui.explanation.textContent = "Edit the source, then compare stages. Analysis is local and stops before execution; imports use your installed Go toolchain.";
  ui.quiz.replaceChildren();
  renderLessonRail();
  scheduleAnalysis(0);
}

function setSource(source) {
  ui.editor.value = source;
  state.selectedRange = null;
  state.selectedAST = -1;
  paintSource();
  scheduleAnalysis(0);
}

function scheduleAnalysis(delay = 280) {
  window.clearTimeout(state.timer);
  state.timer = window.setTimeout(analyze, delay);
}

async function analyze() {
  const sequence = ++state.requestSequence;
  setStatus("Analyzing current source…", "busy");
  ui.output.setAttribute("aria-busy", "true");
  try {
    const response = await fetch("/api/analyze", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: ui.editor.value }),
    });
    const payload = await response.json();
    if (sequence !== state.requestSequence) return;
    if (!response.ok) throw new Error(payload.error || `analysis returned ${response.status}`);
    state.result = payload;
    renderDiagnostics(ui.diagnostics, payload.diagnostics || []);
    renderStage();
    const suffix = payload.diagnostics?.length ? ` with ${payload.diagnostics.length} diagnostic${payload.diagnostics.length === 1 ? "" : "s"}` : "";
    setStatus(`Analysis ready${suffix}`, payload.diagnostics?.length ? "error" : "ready");
  } catch (error) {
    if (sequence !== state.requestSequence) return;
    setStatus(`Analysis failed: ${error.message}`, "error");
    ui.output.replaceChildren(emptyMessage("The local analyzer did not return a result."));
  } finally {
    if (sequence === state.requestSequence) ui.output.removeAttribute("aria-busy");
  }
}

function chooseStage(stage) {
  if (!stages.includes(stage)) return;
  state.stage = stage;
  ui.traceStage.textContent = stage === "ssa" ? "SSA + CFG" : stage;
  for (const button of ui.tabs.querySelectorAll("button[data-stage]")) {
    button.setAttribute("aria-selected", button.dataset.stage === stage ? "true" : "false");
  }
  for (const item of document.querySelectorAll("[data-pipeline]")) item.classList.toggle("is-active", item.dataset.pipeline === stage);
  renderStage();
}

function renderStage() {
  if (!state.result) {
    ui.output.replaceChildren(emptyMessage("Waiting for the first analysis."));
    return;
  }
  const onSelect = (range, astID = -1) => selectRange(range, astID);
  if (state.stage === "tokens") renderTokens(ui.output, state.result, state.selectedRange, onSelect);
  if (state.stage === "ast") renderAST(ui.output, state.result, state.selectedAST, onSelect);
  if (state.stage === "types") renderTypes(ui.output, state.result, state.selectedAST, onSelect);
  if (state.stage === "ssa") renderSSAPlaceholder(ui.output, state.result);
}

function selectRange(range, astID) {
  state.selectedRange = range;
  state.selectedAST = astID;
  paintSource();
  if (range?.line) ui.selection.textContent = `Trace cursor → line ${range.line}:${range.column}, bytes ${range.start}–${range.end}`;
  else ui.selection.textContent = "This item was generated during lowering and has no exact source span.";
  renderStage();
}

function paintSource() {
  const source = ui.editor.value;
  ui.mirror.replaceChildren();
  const range = state.selectedRange;
  if (!range || range.end <= range.start || range.start < 0 || range.end > source.length) {
    ui.mirror.append(document.createTextNode(source + (source.endsWith("\n") ? " " : "\n ")));
    return;
  }
  ui.mirror.append(document.createTextNode(source.slice(0, range.start)));
  const mark = document.createElement("mark");
  mark.textContent = source.slice(range.start, range.end);
  ui.mirror.append(mark, document.createTextNode(source.slice(range.end) + (source.endsWith("\n") ? " " : "\n ")));
}

function renderQuizPlaceholder() {
  ui.quiz.replaceChildren();
  const node = document.createElement("p");
  node.className = "quiz-placeholder";
  node.textContent = "Prediction checkpoint will unlock beside the completed control-flow explorer.";
  ui.quiz.append(node);
}

function setStatus(message, mode) {
  ui.status.textContent = message;
  ui.status.className = `analysis-status is-${mode}`;
}

function loadProgress() {
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY));
    if (parsed?.version === 1 && Array.isArray(parsed.completed)) return parsed;
  } catch (_) {
    // Ignore malformed local-only progress and start cleanly.
  }
  return { version: 1, completed: [], attempts: {} };
}

function emptyMessage(text) {
  const node = document.createElement("div");
  node.className = "empty-state";
  node.textContent = text;
  return node;
}

function span(className, text) {
  const node = document.createElement("span");
  node.className = className;
  node.textContent = text;
  return node;
}
