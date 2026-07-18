import { cfgEdges } from "/model.js";

function make(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function rangeLabel(range) {
  if (!range || !range.line) return "generated";
  return `${range.line}:${range.column} · ${range.start}–${range.end}`;
}

function selectableRow(kind, text, range, selected, onSelect) {
  const li = make("li", "result-row");
  const button = make("button", "result-select");
  button.type = "button";
  button.setAttribute("aria-selected", selected ? "true" : "false");
  button.append(make("span", "result-kind", kind));
  button.append(make("span", "result-text", text || "∅"));
  button.append(make("span", "result-position", rangeLabel(range)));
  button.addEventListener("click", () => onSelect(range));
  li.append(button);
  return li;
}

export function renderTokens(output, result, selected, onSelect) {
  const tokens = result.tokens || [];
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${tokens.length} scanner tokens. Empty rows are automatically inserted semicolons.`));
  const list = make("ol", "result-list");
  for (const token of tokens) {
    const row = selectableRow(token.kind, token.text, token.range, sameRange(selected, token.range), () => onSelect(token.range, -1));
    if (!token.text) row.classList.add("inserted-token");
    list.append(row);
  }
  output.append(list);
}

export function renderAST(output, result, selectedAST, onSelect) {
  const ast = result.ast || [];
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${ast.length} concrete AST nodes in parser preorder. Indentation is syntax containment.`));
  if (!ast.length) return output.append(empty("No AST is available", "Fix the parser diagnostics, then analyze again."));

  const list = make("div", "tree-list");
  const rows = [];
  for (let index = 0; index < ast.length; index += 1) {
    const node = ast[index];
    const next = ast[index + 1];
    const hasChildren = Boolean(next && next.depth > node.depth);
    const row = make("div", "tree-row");
    row.style.setProperty("--depth", String(node.depth));
    row.dataset.depth = String(node.depth);
    const toggle = make("button", "tree-toggle", hasChildren ? "−" : "·");
    toggle.type = "button";
    toggle.disabled = !hasChildren;
    toggle.setAttribute("aria-label", hasChildren ? `Collapse ${node.kind}` : `${node.kind} has no child nodes`);
    toggle.setAttribute("aria-expanded", hasChildren ? "true" : "false");
    if (hasChildren) toggle.addEventListener("click", () => toggleDescendants(rows, index, node.depth, toggle));
    const select = make("button", "result-select");
    select.type = "button";
    select.setAttribute("aria-selected", selectedAST === node.id ? "true" : "false");
    select.append(make("span", "result-kind", node.kind));
    select.append(make("span", "result-text", compact(node.excerpt)));
    select.append(make("span", "result-position", rangeLabel(node.range)));
    select.addEventListener("click", () => onSelect(node.range, node.id));
    row.append(toggle, select);
    rows.push(row);
    list.append(row);
  }
  output.append(list);
}

export function renderTypes(output, result, selectedAST, onSelect) {
  const facts = result.types || [];
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${facts.length} semantic facts from types.Info. One occurrence can have more than one fact.`));
  if (!facts.length) return output.append(empty("No type facts are available", "Type checking must succeed before semantic facts appear."));
  const list = make("ol", "result-list");
  for (const fact of facts) {
    const li = make("li", "result-row fact-row");
    const button = make("button", "result-select");
    button.type = "button";
    button.setAttribute("aria-selected", selectedAST === fact.astId ? "true" : "false");
    button.append(make("span", "result-kind", fact.kind));
    button.append(make("span", "result-text", compact(fact.name)));
    const detail = make("span", "fact-detail");
    detail.append(make("strong", "", fact.value ? `${fact.type} = ${fact.value}` : fact.type || "—"));
    if (fact.object) detail.append(make("small", "", fact.object));
    button.append(detail);
    button.addEventListener("click", () => onSelect(fact.range, fact.astId));
    li.append(button);
    list.append(li);
  }
  output.append(list);
}

export function renderSSA(output, result, selectedAST, onSelect, preferredName, onFunction) {
  output.replaceChildren();
  const functions = result.functions || [];
  if (!functions.length) {
    output.append(empty("No SSA is available", "SSA construction begins only after type checking succeeds."));
    return;
  }

  const preferred = functions.find((fn) => fn.name === preferredName);
  const functionItem = preferred || functions.find((fn) => fn.name !== "init") || functions[0];
  const toolbar = make("div", "ssa-toolbar");
  const label = make("label", "", "Function");
  label.htmlFor = "ssa-function";
  const select = make("select", "function-select");
  select.id = "ssa-function";
  for (const fn of functions) {
    const option = make("option", "", `${fn.name} ${fn.signature}`);
    option.value = fn.name;
    option.selected = fn === functionItem;
    select.append(option);
  }
  select.addEventListener("change", () => onFunction(select.value));
  toolbar.append(label, select);
  output.append(toolbar);

  const blocks = functionItem.blocks || [];
  const edges = cfgEdges(blocks);
  output.append(make("p", "stage-summary", `${blocks.length} basic blocks · ${edges.length} control-flow edges · ${countInstructions(blocks)} instructions`));
  if (!blocks.length) {
    output.append(empty("This function has no source body", "Choose a function with basic blocks."));
    return;
  }

  const summary = make("p", "sr-only", `Control-flow graph for ${functionItem.name}. ${edges.map((edge) => `Block ${edge.from} flows to block ${edge.to}${edge.label ? ` on ${edge.label}` : ""}.`).join(" ")}`);
  const flow = make("div", "cfg-flow");
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.classList.add("cfg-edges");
  svg.setAttribute("aria-hidden", "true");
  const list = make("div", "cfg-blocks");
  for (const block of blocks) {
    const predecessors = block.preds || [];
    const successors = block.succs || [];
    const blockInstructions = block.instructions || [];
    const card = make("section", "cfg-block");
    card.dataset.block = String(block.index);
    const header = make("header", "cfg-block-heading");
    const title = make("h3", "", `BB${block.index}${block.comment ? ` · ${block.comment}` : ""}`);
    const edgeText = `pred [${predecessors.join(", ") || "—"}] → succ [${successors.join(", ") || "—"}]`;
    header.append(title, make("span", "", edgeText));
    card.append(header);
    const instructions = make("ol", "instruction-list");
    if (!blockInstructions.length) instructions.append(make("li", "instruction-empty", "No instructions"));
    for (const instruction of blockInstructions) {
      const li = make("li", instruction.kind === "Phi" ? "instruction-row is-phi" : "instruction-row");
      const button = make("button", "instruction-select");
      button.type = "button";
      button.setAttribute("aria-selected", selectedAST >= 0 && selectedAST === instruction.astId ? "true" : "false");
      button.append(make("span", "instruction-kind", instruction.kind));
      button.append(make("code", "", instruction.text));
      const meta = instruction.result ? `${instruction.result}${instruction.type ? ` · ${instruction.type}` : ""}` : "effect only";
      button.append(make("span", "instruction-meta", meta));
      button.addEventListener("click", () => onSelect(instruction.range, instruction.astId));
      li.append(button);
      instructions.append(li);
    }
    card.append(instructions);
    list.append(card);
  }
  flow.append(svg, list);
  output.append(summary, flow);
  window.requestAnimationFrame(() => drawEdges(flow, svg, edges));

  const raw = make("details", "raw-ssa");
  raw.append(make("summary", "", "Read raw x/tools SSA"));
  const pre = make("pre");
  pre.append(make("code", "", functionItem.raw));
  raw.append(pre);
  output.append(raw);
}

export function renderDiagnostics(container, diagnostics) {
  container.replaceChildren();
  container.hidden = diagnostics.length === 0;
  if (!diagnostics.length) return;
  container.append(make("h2", "", "The pipeline stopped with diagnostics"));
  const list = make("ul");
  for (const diagnostic of diagnostics) {
    const where = diagnostic.line ? ` ${diagnostic.line}:${diagnostic.column}` : "";
    list.append(make("li", "", `${diagnostic.phase}${where} — ${diagnostic.message}`));
  }
  container.append(list);
}

function toggleDescendants(rows, index, depth, toggle) {
  const collapsing = toggle.getAttribute("aria-expanded") === "true";
  toggle.setAttribute("aria-expanded", collapsing ? "false" : "true");
  toggle.textContent = collapsing ? "+" : "−";
  for (let cursor = index + 1; cursor < rows.length; cursor += 1) {
    const childDepth = Number(rows[cursor].dataset.depth);
    if (childDepth <= depth) break;
    rows[cursor].hidden = collapsing;
  }
}

function empty(title, detail) {
  const node = make("div", "empty-state");
  node.append(make("strong", "", title), document.createTextNode(detail));
  return node;
}

function compact(text) {
  const value = String(text || "").replace(/\s+/g, " ").trim();
  return value.length > 72 ? `${value.slice(0, 69)}…` : value || "∅";
}

function sameRange(left, right) {
  return Boolean(left && right && left.start === right.start && left.end === right.end);
}

function countInstructions(blocks) {
  return blocks.reduce((sum, block) => sum + (block.instructions || []).length, 0);
}

function drawEdges(flow, svg, edges) {
  svg.replaceChildren();
  const flowRect = flow.getBoundingClientRect();
  const cards = new Map();
  for (const card of flow.querySelectorAll("[data-block]")) cards.set(Number(card.dataset.block), card);
  const height = Math.max(flow.scrollHeight, flow.clientHeight);
  const width = Math.max(flow.clientWidth, 1);
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("width", String(width));
  svg.setAttribute("height", String(height));
  edges.forEach((edge, index) => {
    const from = cards.get(edge.from);
    const to = cards.get(edge.to);
    if (!from || !to) return;
    const fromRect = from.getBoundingClientRect();
    const toRect = to.getBoundingClientRect();
    const startX = fromRect.left - flowRect.left;
    const startY = fromRect.top - flowRect.top + fromRect.height / 2;
    const endX = toRect.left - flowRect.left;
    const endY = toRect.top - flowRect.top + toRect.height / 2;
    const laneX = Math.max(12, startX - 24 - (index % 3) * 12);
    const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
    path.setAttribute("d", `M ${startX} ${startY} C ${laneX} ${startY}, ${laneX} ${endY}, ${endX} ${endY}`);
    path.setAttribute("class", edge.to <= edge.from ? "cfg-edge is-back" : "cfg-edge");
    svg.append(path);
    if (edge.label) {
      const text = document.createElementNS("http://www.w3.org/2000/svg", "text");
      text.setAttribute("x", String(laneX + 2));
      text.setAttribute("y", String((startY + endY) / 2 - 4));
      text.setAttribute("class", "cfg-edge-label");
      text.textContent = edge.label;
      svg.append(text);
    }
  });
}
