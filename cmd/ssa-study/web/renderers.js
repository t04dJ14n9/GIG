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
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${result.tokens.length} scanner tokens. Empty rows are automatically inserted semicolons.`));
  const list = make("ol", "result-list");
  for (const token of result.tokens) {
    const row = selectableRow(token.kind, token.text, token.range, sameRange(selected, token.range), () => onSelect(token.range, -1));
    if (!token.text) row.classList.add("inserted-token");
    list.append(row);
  }
  output.append(list);
}

export function renderAST(output, result, selectedAST, onSelect) {
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${result.ast.length} concrete AST nodes in parser preorder. Indentation is syntax containment.`));
  if (!result.ast.length) return output.append(empty("No AST is available", "Fix the parser diagnostics, then analyze again."));

  const list = make("div", "tree-list");
  const rows = [];
  for (let index = 0; index < result.ast.length; index += 1) {
    const node = result.ast[index];
    const next = result.ast[index + 1];
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
  output.replaceChildren();
  output.append(make("p", "stage-summary", `${result.types.length} semantic facts from types.Info. One occurrence can have more than one fact.`));
  if (!result.types.length) return output.append(empty("No type facts are available", "Type checking must succeed before semantic facts appear."));
  const list = make("ol", "result-list");
  for (const fact of result.types) {
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

export function renderSSAPlaceholder(output, result) {
  output.replaceChildren();
  if (!result.functions.length) {
    output.append(empty("No SSA is available", "SSA construction begins only after type checking succeeds."));
    return;
  }
  output.append(empty("SSA is ready", `${result.functions.length} functions were built. The control-flow explorer is the final guided lesson.`));
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
