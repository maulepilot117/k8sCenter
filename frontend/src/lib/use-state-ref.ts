import { useState } from "react";

/**
 * State whose current value is also readable from a ref, for code that runs
 * between a state change and the next render: a once-registered socket or
 * keyboard handler, an animation-frame flush, or the rest of an async
 * function after an await. The setter writes the ref first, so such code sees
 * the new value at once instead of the one from the render that set it up.
 * The setter and the ref keep the same identity for the component's
 * lifetime.
 */
export function useStateRef<T>(
  initial: T,
): [T, (next: T) => void, { current: T }] {
  const [state, setState] = useState(() => initial);
  const [holder] = useState(() => {
    const ref = { current: initial };
    const set = (next: T) => {
      ref.current = next;
      setState(() => next);
    };
    return { ref, set };
  });
  return [state, holder.set, holder.ref];
}
