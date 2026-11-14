// The last lines of a task log, as the failure reason the summary shows when
// a task failed before any host reported a result (for example while cloning
// the repository or installing requirements). The log tab shows the same
// lines to the same people.
// Terminal colour sequences start with the escape character on purpose.
// eslint-disable-next-line no-control-regex
const ANSI = /\u001b\[[0-9;]*[A-Za-z]/g;

export function taskFailureLines(output, count = 5) {
  return (output || [])
    .map((line) => String(line?.output ?? '').replace(ANSI, '').trimEnd())
    .filter((line) => line.trim() !== '')
    .slice(-count);
}

export default taskFailureLines;
