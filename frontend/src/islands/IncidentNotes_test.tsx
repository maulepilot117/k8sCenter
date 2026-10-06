/** @jsxImportSource preact */
import { afterAll, afterEach, beforeAll, expect, test } from "bun:test";
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { render } from "preact";
import { act } from "preact/test-utils";
import type { NoteView } from "@/lib/incident-types.ts";

const { default: IncidentNotes } = await import("./IncidentNotes.tsx");

beforeAll(() => GlobalRegistrator.register());
afterAll(() => GlobalRegistrator.unregister());

const ID = "00000000-0000-4000-8000-000000000001";
const N1 = "00000000-0000-4000-8000-0000000000a1";
const N2 = "00000000-0000-4000-8000-0000000000a2";

interface Call {
  url: string;
  method: string;
  body?: string;
}
type Reply = Response | Promise<Response> | undefined;
type Route = (call: Call) => Reply;

let calls: Call[] = [];
let host: HTMLElement | null = null;
let originalFetch: typeof globalThis.fetch | undefined;

afterEach(() => {
  if (host) {
    act(() => render(null, host as HTMLElement));
    host.remove();
    host = null;
  }
  if (originalFetch) globalThis.fetch = originalFetch;
  originalFetch = undefined;
});

const json = (status: number, body: unknown) =>
  new Response(status === 204 ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

function stubFetch(...routes: Route[]) {
  calls = [];
  originalFetch = globalThis.fetch;
  globalThis.fetch = ((input: string | URL | Request, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? init.body : undefined,
    };
    calls.push(call);
    for (const r of routes) {
      const res = r(call);
      if (res) return Promise.resolve(res);
    }
    return Promise.resolve(
      json(500, { error: { code: 500, message: "unrouted" } }),
    );
  }) as typeof globalThis.fetch;
}

const flush = () =>
  act(async () => {
    for (let i = 0; i < 6; i++) await new Promise((r) => setTimeout(r, 0));
  });

async function mount(currentUserId = "alice", canAnnotate = true) {
  host = document.createElement("div");
  document.body.appendChild(host);
  act(() =>
    render(
      <IncidentNotes
        incidentId={ID}
        canAnnotate={canAnnotate}
        currentUserId={currentUserId}
      />,
      host as HTMLElement,
    ),
  );
  await flush();
  return host;
}

function note(id: string, over: Partial<NoteView> = {}): NoteView {
  return {
    id,
    incidentId: ID,
    authorId: "alice",
    body: `body of ${id}`,
    revision: 1,
    createdAt: "2026-10-01T10:00:00Z",
    updatedAt: "2026-10-01T10:00:00Z",
    ...over,
  };
}

const button = (root: HTMLElement, label: string) =>
  [...root.querySelectorAll("button")].find((b) => b.textContent === label);
const click = async (root: HTMLElement, label: string) => {
  act(() => button(root, label)?.click());
  await flush();
};
const type = (el: HTMLTextAreaElement, value: string) =>
  act(() => {
    el.value = value;
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
const editor = (root: HTMLElement, id: string) =>
  root.querySelector(`textarea#note-edit-${id}`) as HTMLTextAreaElement | null;
const conflict = (current: number) =>
  json(409, {
    error: {
      code: 409,
      message: "note was modified since it was read",
      reason: "note_revision_conflict",
      extra: { currentRevision: current },
    },
  });

/** A notes list whose pages are served from `pages` by cursor. */
const listRoute =
  (pages: () => Record<string, { items: NoteView[]; next?: string }>): Route =>
  (c) => {
    if (c.method !== "GET" || !c.url.includes("/notes")) return undefined;
    const cursor = new URL(c.url, "http://x").searchParams.get("continue");
    const page = pages()[cursor ?? ""];
    return json(200, {
      data: page.items,
      metadata: page.next ? { continue: page.next } : {},
    });
  };

test("a conflict keeps the draft, labels the cached text honestly, and the next save carries the reloaded revision", async () => {
  let server = note(N1);
  const puts: Call[] = [];
  stubFetch(
    listRoute(() => ({ "": { items: [server] } })),
    (c) => {
      if (c.method !== "PUT") return undefined;
      puts.push(c);
      return puts.length === 1
        ? conflict(3)
        : json(200, { data: { ...server, body: "mine", revision: 4 } });
    },
  );
  const root = await mount();
  await click(root, "Edit");
  type(editor(root, N1) as HTMLTextAreaElement, "my careful rewrite");
  // Someone else saves revision 3 meanwhile.
  server = note(N1, { body: "their text", revision: 3 });
  await click(root, "Save");

  expect(JSON.parse(puts[0].body ?? "{}")).toEqual({
    body: "my careful rewrite",
    revision: 1,
  });
  expect(root.textContent).toContain("revision 3");
  expect(root.textContent).toContain("Your last loaded copy");
  expect(root.textContent).not.toContain("Current saved text");
  expect(editor(root, N1)?.value).toBe("my careful rewrite");

  await click(root, "Reload notes");
  expect(root.textContent).toContain("Current saved text");
  expect(root.textContent).toContain("their text");
  expect(editor(root, N1)?.value).toBe("my careful rewrite");

  await click(root, "Save");
  expect(JSON.parse(puts[1].body ?? "{}")).toEqual({
    body: "my careful rewrite",
    revision: 3,
  });
  expect(editor(root, N1)).toBeNull();
});

test("the editor stays, with the draft, when a reload leaves the edited note on a later page", async () => {
  let full = false;
  stubFetch(
    listRoute(() => ({
      "": { items: [note(N2)], next: "p2" },
      p2: { items: [note(N1, { revision: full ? 2 : 1 })] },
    })),
    (c) => (c.method === "PUT" ? conflict(2) : undefined),
  );
  const root = await mount();
  await click(root, "Load more notes");
  const edits = [...root.querySelectorAll("button")].filter(
    (b) => b.textContent === "Edit",
  );
  act(() => edits[1].click());
  await flush();
  type(editor(root, N1) as HTMLTextAreaElement, "draft on page two");
  full = true;
  await click(root, "Save");
  await click(root, "Reload notes");

  // Page one no longer holds the note, but the editor and draft remain.
  expect(root.querySelectorAll("ol[aria-label='Notes'] > li")).toHaveLength(1);
  expect(editor(root, N1)?.value).toBe("draft on page two");
  expect(root.textContent).toContain("not in the loaded list");
  expect(root.textContent).toContain("Your last loaded copy");
});

test("Load more notes appends the next page", async () => {
  stubFetch(
    listRoute(() => ({
      "": { items: [note(N1)], next: "p2" },
      p2: { items: [note(N2, { authorId: "bob" })] },
    })),
  );
  const root = await mount();
  await click(root, "Load more notes");
  expect(calls.map((c) => c.url)).toEqual([
    `/api/v1/incidents/${ID}/notes?limit=50`,
    `/api/v1/incidents/${ID}/notes?limit=50&continue=p2`,
  ]);
  expect(root.querySelectorAll("ol[aria-label='Notes'] > li")).toHaveLength(2);
  expect(button(root, "Load more notes")).toBeUndefined();
});

test("a new note is appended when the thread is fully loaded", async () => {
  stubFetch(
    listRoute(() => ({ "": { items: [] } })),
    (c) =>
      c.method === "POST"
        ? json(201, { data: note(N1, { body: "first" }) })
        : undefined,
  );
  const root = await mount();
  type(
    root.querySelector(`textarea#new-note-${ID}`) as HTMLTextAreaElement,
    "first",
  );
  const form = root.querySelector("form") as HTMLFormElement;
  act(() => {
    form.dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true }),
    );
  });
  await flush();
  const post = calls.find((c) => c.method === "POST");
  expect(post?.url).toBe(`/api/v1/incidents/${ID}/notes`);
  expect(JSON.parse(post?.body ?? "{}")).toEqual({ body: "first" });
  expect(root.querySelector("ol[aria-label='Notes']")?.textContent).toContain(
    "first",
  );
});

test("a new note is not appended out of order while more pages remain", async () => {
  stubFetch(
    listRoute(() => ({ "": { items: [note(N2)], next: "p2" } })),
    (c) =>
      c.method === "POST"
        ? json(201, { data: note(N1, { body: "newest" }) })
        : undefined,
  );
  const root = await mount();
  type(
    root.querySelector(`textarea#new-note-${ID}`) as HTMLTextAreaElement,
    "newest",
  );
  act(() => {
    root
      .querySelector("form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
  await flush();
  expect(root.querySelectorAll("ol[aria-label='Notes'] > li")).toHaveLength(1);
  expect(root.textContent).toContain("appears at the end of the thread");
});

test("a failed note create shows the error and keeps the draft", async () => {
  stubFetch(
    listRoute(() => ({ "": { items: [] } })),
    (c) =>
      c.method === "POST"
        ? json(403, {
            error: {
              code: 403,
              message: "this grant does not allow annotating the incident",
            },
          })
        : undefined,
  );
  const root = await mount();
  const area = root.querySelector(
    `textarea#new-note-${ID}`,
  ) as HTMLTextAreaElement;
  type(area, "kept");
  act(() => {
    root
      .querySelector("form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
  await flush();
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "does not allow annotating",
  );
  expect(area.value).toBe("kept");
});

test("Confirm delete double-clicked sends one DELETE; a 404 counts as deleted", async () => {
  let release: () => void = () => {};
  stubFetch(
    listRoute(() => ({ "": { items: [note(N1)] } })),
    (c) =>
      c.method === "DELETE"
        ? new Promise<Response>((resolve) => {
            release = () =>
              resolve(
                json(404, {
                  error: { code: 404, message: "incident note not found" },
                }),
              );
          })
        : undefined,
  );
  const root = await mount();
  await click(root, "Delete");
  const confirm = button(root, "Confirm delete");
  act(() => {
    confirm?.click();
    confirm?.click();
  });
  await flush();
  expect(button(root, "Deleting…")?.getAttribute("aria-disabled")).toBe("true");
  release();
  await flush();
  const deletes = calls.filter((c) => c.method === "DELETE");
  expect(deletes).toHaveLength(1);
  expect(deletes[0].url).toBe(`/api/v1/incidents/${ID}/notes/${N1}`);
  expect(root.querySelector("ol[aria-label='Notes']")).toBeNull();
  expect(root.querySelector('[role="alert"]')).toBeNull();
});

test("a failed delete shows its error and keeps the note", async () => {
  stubFetch(
    listRoute(() => ({ "": { items: [note(N1)] } })),
    (c) =>
      c.method === "DELETE"
        ? json(503, { error: { code: 503, message: "store unavailable" } })
        : undefined,
  );
  const root = await mount();
  await click(root, "Delete");
  await click(root, "Confirm delete");
  expect(root.querySelector('[role="alert"]')?.textContent).toContain(
    "The note could not be deleted.",
  );
  expect(root.querySelectorAll("ol[aria-label='Notes'] > li")).toHaveLength(1);
});

test("the edit textarea is read-only while a save is in flight", async () => {
  let release: () => void = () => {};
  stubFetch(
    listRoute(() => ({ "": { items: [note(N1)] } })),
    (c) =>
      c.method === "PUT"
        ? new Promise<Response>((resolve) => {
            release = () =>
              resolve(
                json(200, { data: note(N1, { body: "x", revision: 2 }) }),
              );
          })
        : undefined,
  );
  const root = await mount();
  await click(root, "Edit");
  type(editor(root, N1) as HTMLTextAreaElement, "x");
  await click(root, "Save");
  expect(editor(root, N1)?.disabled).toBe(true);
  release();
  await flush();
  expect(editor(root, N1)).toBeNull();
});

test("another author's note offers no edit or delete", async () => {
  stubFetch(listRoute(() => ({ "": { items: [note(N1)] } })));
  const root = await mount("bob");
  const labels = [...root.querySelectorAll("button")].map((b) => b.textContent);
  expect(labels).not.toContain("Edit");
  expect(labels).not.toContain("Delete");
});

// --- Review round 2 ---------------------------------------------------------

const submitNew = (root: HTMLElement, text: string) => {
  type(
    root.querySelector(`textarea#new-note-${ID}`) as HTMLTextAreaElement,
    text,
  );
  act(() => {
    root
      .querySelector("form")
      ?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
};

test("a note created while the first load is in flight survives that load", async () => {
  let releaseList: () => void = () => {};
  stubFetch(
    (c) =>
      c.method === "GET" && c.url.includes("/notes")
        ? new Promise<Response>((resolve) => {
            // The load read the thread before the note existed.
            releaseList = () => resolve(json(200, { data: [], metadata: {} }));
          })
        : undefined,
    (c) =>
      c.method === "POST"
        ? json(201, { data: note(N1, { body: "written early" }) })
        : undefined,
  );
  const root = await mount();
  submitNew(root, "written early");
  await flush();
  releaseList();
  await flush();
  expect(root.querySelector("ol[aria-label='Notes']")?.textContent).toContain(
    "written early",
  );
  expect(root.textContent).not.toContain("No notes yet.");
});

test("a failed first load offers Retry, and a note posted meanwhile is reported", async () => {
  let lists = 0;
  stubFetch(
    (c) => {
      if (c.method !== "GET" || !c.url.includes("/notes")) return undefined;
      lists++;
      return lists === 1
        ? json(503, { error: { code: 503, message: "store unavailable" } })
        : json(200, { data: [note(N1, { body: "after" })], metadata: {} });
    },
    (c) =>
      c.method === "POST"
        ? json(201, { data: note(N1, { body: "after" }) })
        : undefined,
  );
  const root = await mount();
  expect(root.textContent).toContain("Could not load notes.");
  submitNew(root, "after");
  await flush();
  expect(root.textContent).toContain("Note added. Load the notes to see it.");
  expect(root.querySelector("ol[aria-label='Notes']")).toBeNull();
  await click(root, "Retry");
  expect(lists).toBe(2);
  expect(root.querySelector("ol[aria-label='Notes']")?.textContent).toContain(
    "after",
  );
});

test("the new-note textarea is read-only while a post is in flight", async () => {
  let release: () => void = () => {};
  stubFetch(
    listRoute(() => ({ "": { items: [] } })),
    (c) =>
      c.method === "POST"
        ? new Promise<Response>((resolve) => {
            release = () => resolve(json(201, { data: note(N1) }));
          })
        : undefined,
  );
  const root = await mount();
  submitNew(root, "x");
  await flush();
  const area = root.querySelector(
    `textarea#new-note-${ID}`,
  ) as HTMLTextAreaElement;
  expect(area.disabled).toBe(true);
  release();
  await flush();
  expect(area.disabled).toBe(false);
});

test("a stale page issued before the 409 does not rebind the editor's revision", async () => {
  let releasePage2: () => void = () => {};
  const puts: Call[] = [];
  stubFetch(
    (c) => {
      if (c.method !== "GET" || !c.url.includes("/notes")) return undefined;
      if (c.url.includes("continue=p2")) {
        // Read before the conflicting save: still revision 1.
        return new Promise<Response>((resolve) => {
          releasePage2 = () =>
            resolve(json(200, { data: [note(N1)], metadata: {} }));
        });
      }
      return json(200, { data: [note(N1)], metadata: { continue: "p2" } });
    },
    (c) => {
      if (c.method !== "PUT") return undefined;
      puts.push(c);
      return conflict(2);
    },
  );
  const root = await mount();
  await click(root, "Edit");
  type(editor(root, N1) as HTMLTextAreaElement, "mine");
  await click(root, "Load more notes");
  await click(root, "Save");
  expect(root.textContent).toContain("Your last loaded copy");
  releasePage2();
  await flush();
  // The old page neither marks the text current nor moves the revision.
  expect(root.textContent).toContain("Your last loaded copy");
  await click(root, "Save");
  expect(JSON.parse(puts[1].body ?? "{}").revision).toBe(1);
});

test("Edit on another note while a save is pending does not hijack the editor", async () => {
  let release: () => void = () => {};
  stubFetch(
    listRoute(() => ({ "": { items: [note(N1), note(N2)] } })),
    (c) =>
      c.method === "PUT"
        ? new Promise<Response>((resolve) => {
            release = () =>
              resolve(
                json(200, { data: note(N1, { body: "saved", revision: 2 }) }),
              );
          })
        : undefined,
  );
  const root = await mount();
  const edits = () =>
    [...root.querySelectorAll("button")].filter(
      (b) => b.textContent === "Edit",
    );
  act(() => edits()[0].click());
  await flush();
  type(editor(root, N1) as HTMLTextAreaElement, "saved");
  await click(root, "Save");
  // N2's Edit while N1's save is pending is refused.
  act(() => edits()[0].click());
  await flush();
  expect(editor(root, N2)).toBeNull();
  expect(editor(root, N1)?.disabled).toBe(true);
  release();
  await flush();
  expect(editor(root, N1)).toBeNull();
  // Now N2 can be edited, and nothing from N1's save touches it.
  act(() => edits()[1].click());
  await flush();
  expect(editor(root, N2)?.value).toBe(`body of ${N2}`);
});

// --- Review round 3 ---------------------------------------------------------

/** A list route whose "p2" page is held until the test releases it. */
function heldPageTwo(page2: () => { items: NoteView[]; next?: string }) {
  let release: () => void = () => {};
  const route: Route = (c) => {
    if (c.method !== "GET" || !c.url.includes("/notes")) return undefined;
    if (!c.url.includes("continue=p2")) {
      return json(200, { data: [note(N2)], metadata: { continue: "p2" } });
    }
    return new Promise<Response>((resolve) => {
      release = () => {
        const p = page2();
        resolve(
          json(200, {
            data: p.items,
            metadata: p.next ? { continue: p.next } : {},
          }),
        );
      };
    });
  };
  return { route, release: () => release() };
}
const createRoute: Route = (c) =>
  c.method === "POST"
    ? json(201, { data: note(N1, { body: "made mid-page" }) })
    : undefined;

test("a note held during Load more is appended when that page completes the thread", async () => {
  const p2 = heldPageTwo(() => ({ items: [] }));
  stubFetch(p2.route, createRoute);
  const root = await mount();
  await click(root, "Load more notes");
  submitNew(root, "made mid-page");
  await flush();
  // Held: not yet in the list while the page is in flight.
  expect(
    root.querySelector("ol[aria-label='Notes']")?.textContent,
  ).not.toContain("made mid-page");
  p2.release();
  await flush();
  const items = [...root.querySelectorAll("ol[aria-label='Notes'] > li")];
  expect(items).toHaveLength(2);
  expect(items[1].textContent).toContain("made mid-page");
});

test("a note held during Load more is reported when more pages still follow", async () => {
  const p2 = heldPageTwo(() => ({ items: [], next: "p3" }));
  stubFetch(p2.route, createRoute);
  const root = await mount();
  await click(root, "Load more notes");
  submitNew(root, "made mid-page");
  await flush();
  p2.release();
  await flush();
  expect(
    root.querySelector("ol[aria-label='Notes']")?.textContent,
  ).not.toContain("made mid-page");
  expect(root.textContent).toContain("appears at the end of the thread");
});

test("a note held for one incident is never placed into another incident's thread", async () => {
  const ID2 = "00000000-0000-4000-8000-000000000002";
  let releaseFirst: () => void = () => {};
  stubFetch((c) => {
    if (c.method !== "GET" || !c.url.includes("/notes")) return undefined;
    if (c.url.includes(ID2)) return json(200, { data: [], metadata: {} });
    return new Promise<Response>((resolve) => {
      releaseFirst = () => resolve(json(200, { data: [], metadata: {} }));
    });
  }, createRoute);
  const root = await mount();
  submitNew(root, "made mid-page");
  await flush();
  // The view switches to another incident while the first load is held.
  act(() =>
    render(
      <IncidentNotes incidentId={ID2} canAnnotate currentUserId="alice" />,
      host as HTMLElement,
    ),
  );
  await flush();
  releaseFirst();
  await flush();
  expect(root.textContent).not.toContain("made mid-page");
  expect(root.textContent).toContain("No notes yet.");
});
