export function gradePrediction(quiz, selected) {
  const correct = selected === quiz.answer;
  return { correct, feedback: correct ? quiz.correct : quiz.incorrect };
}

export function recordAttempt(progress, lessonID, correct) {
  const next = {
    version: 1,
    completed: [...(progress.completed || [])],
    attempts: { ...(progress.attempts || {}) },
  };
  next.attempts[lessonID] = (next.attempts[lessonID] || 0) + 1;
  if (correct && !next.completed.includes(lessonID)) next.completed.push(lessonID);
  return next;
}

export function cfgEdges(blocks) {
  const edges = [];
  for (const block of blocks) {
    const successors = block.succs || [];
    for (let index = 0; index < successors.length; index += 1) {
      let label = "";
      if (successors.length === 2) label = index === 0 ? "true" : "false";
      edges.push({ from: block.index, to: successors[index], label });
    }
  }
  return edges;
}
