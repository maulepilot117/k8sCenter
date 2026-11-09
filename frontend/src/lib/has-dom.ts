/**
 * Whether a DOM exists right now.
 *
 * Call it where the answer is needed (inside an effect), never once at module
 * load: a module first imported where there is no DOM (a server render, a
 * test file that imports before registering one) would otherwise keep
 * answering "no browser" for the rest of the process.
 */
export function hasDom(): boolean {
  return typeof document !== "undefined";
}
