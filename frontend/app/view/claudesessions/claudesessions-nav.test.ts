// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
    buildRows,
    displayName,
    edgeSelection,
    foldAction,
    formatAge,
    groupKey,
    isHidden,
    launchKeyAction,
    moveSelection,
    sessionKey,
    sessionState,
    shortenPath,
} from "./claudesessions-nav";

function mk(id: string, cwd: string, lastactive: number, extra: Partial<ClaudeSession> = {}): ClaudeSession {
    return { harness: "claude", sessionid: id, cwd, lastactive, state: "offline", ...extra };
}

const sessions: ClaudeSession[] = [
    mk("a", "/p/one", 100, { name: "alpha" }),
    mk("b", "/p/one", 300, { pid: 5, state: "busy" }),
    mk("c", "/p/two", 200, { name: "gamma", preview: "fix the parser" }),
    mk("d", "/p/two", 50),
];
const opts = { collapsed: new Set<string>(), filter: "", showOffline: true, descriptions: {} };

describe("buildRows", () => {
    it("groups by folder in alphabetical order, running sessions first", () => {
        const rows = buildRows(sessions, opts);
        expect(rows.map((r) => r.key)).toEqual([
            groupKey("/p/one"),
            sessionKey("b"),
            sessionKey("a"),
            groupKey("/p/two"),
            sessionKey("c"),
            sessionKey("d"),
        ]);
        expect(rows[0]).toMatchObject({ kind: "group", count: 2, running: 1 });
    });

    it("hides offline sessions and drops empty groups", () => {
        const rows = buildRows(sessions, { ...opts, showOffline: false });
        expect(rows.map((r) => r.key)).toEqual([groupKey("/p/one"), sessionKey("b")]);
    });

    it("hides the rows of a collapsed group", () => {
        const rows = buildRows(sessions, { ...opts, collapsed: new Set(["/p/one"]) });
        expect(rows.map((r) => r.key)).toEqual([
            groupKey("/p/one"),
            groupKey("/p/two"),
            sessionKey("c"),
            sessionKey("d"),
        ]);
    });

    it("filters on name, id, folder, preview and description, and opens collapsed groups", () => {
        const collapsed = new Set(["/p/two"]);
        expect(buildRows(sessions, { ...opts, collapsed, filter: "parser" }).map((r) => r.key)).toEqual([
            groupKey("/p/two"),
            sessionKey("c"),
        ]);
        expect(buildRows(sessions, { ...opts, filter: "ALPHA" }).map((r) => r.key)).toEqual([
            groupKey("/p/one"),
            sessionKey("a"),
        ]);
        expect(
            buildRows(sessions, { ...opts, filter: "notes", descriptions: { d: "my notes" } }).map((r) => r.key)
        ).toEqual([groupKey("/p/two"), sessionKey("d")]);
    });

    it("puts sessions without a folder in one unknown group", () => {
        const rows = buildRows([mk("x", undefined, 1)], opts);
        expect(rows[0]).toMatchObject({ kind: "group", cwd: "" });
    });

    it("copes with no sessions", () => {
        expect(buildRows(null, opts)).toEqual([]);
    });
});

describe("sessionState", () => {
    it("takes the state the backend reports, offline when it is missing or unknown", () => {
        expect(sessionState(mk("a", "/", 1, { state: "waiting" }))).toBe("waiting");
        expect(sessionState(mk("a", "/", 1, { state: "idle" }))).toBe("idle");
        expect(sessionState(mk("a", "/", 1, { state: "bogus" }))).toBe("offline");
        expect(sessionState(mk("a", "/", 1, { state: undefined }))).toBe("offline");
    });

    it("marks a live session started outside Bifrost as external, and does not count it as running", () => {
        const ext = mk("e", "/p", 1, { state: "idle", external: true });
        expect(sessionState(ext)).toBe("external");
        expect(sessionState(mk("o", "/p", 1, { state: "offline", external: true }))).toBe("offline");
        const rows = buildRows([ext, mk("w", "/p", 2, { state: "idle" })], opts);
        expect(rows[0]).toMatchObject({ kind: "group", count: 2, running: 1 });
    });

    it("counts a waiting session as running", () => {
        const rows = buildRows([mk("w", "/p", 1, { state: "waiting" })], opts);
        expect(rows[0]).toMatchObject({ kind: "group", running: 1 });
    });
});

describe("folder order", () => {
    it("is alphabetical and does not change when activity changes", () => {
        const a = [mk("1", "/b", 100), mk("2", "/A", 999), mk("3", "/c", 500, { state: "busy", pid: 1 })];
        const order = (ss: ClaudeSession[]) =>
            buildRows(ss, opts)
                .filter((r) => r.kind === "group")
                .map((g) => g.cwd);
        expect(order(a)).toEqual(["/A", "/b", "/c"]);
        const b = [mk("1", "/b", 5000, { state: "busy" }), mk("2", "/A", 1), mk("3", "/c", 1)];
        expect(order(b)).toEqual(["/A", "/b", "/c"]);
    });

    it("puts sessions without a folder last", () => {
        const rows = buildRows([mk("x", undefined, 9999), mk("y", "/z", 1)], opts);
        expect(rows.filter((r) => r.kind === "group").map((g) => g.cwd)).toEqual(["/z", ""]);
    });
});

describe("selection", () => {
    const rows = buildRows(sessions, opts);

    it("moves and clamps", () => {
        expect(moveSelection(rows, sessionKey("b"), 1)).toBe(sessionKey("a"));
        expect(moveSelection(rows, sessionKey("d"), 1)).toBe(sessionKey("d"));
        expect(moveSelection(rows, groupKey("/p/one"), -1)).toBe(groupKey("/p/one"));
        expect(moveSelection(rows, sessionKey("a"), 99)).toBe(sessionKey("d"));
    });

    it("lands on the first row when nothing valid is selected", () => {
        expect(moveSelection(rows, "s:gone", 1)).toBe(groupKey("/p/one"));
        expect(moveSelection([], null, 1)).toBeNull();
    });

    it("finds the edges", () => {
        expect(edgeSelection(rows, false)).toBe(groupKey("/p/one"));
        expect(edgeSelection(rows, true)).toBe(sessionKey("d"));
    });

    it("folds with left and right", () => {
        expect(foldAction(rows, sessionKey("a"), "left")).toEqual({ select: groupKey("/p/one") });
        expect(foldAction(rows, groupKey("/p/one"), "left")).toEqual({ toggle: "/p/one" });
        expect(foldAction(rows, groupKey("/p/one"), "right")).toEqual({ select: sessionKey("b") });
        const closed = buildRows(sessions, { ...opts, collapsed: new Set(["/p/one"]) });
        expect(foldAction(closed, groupKey("/p/one"), "right")).toEqual({ toggle: "/p/one" });
        expect(foldAction(closed, groupKey("/p/one"), "left")).toEqual({});
        expect(foldAction(rows, sessionKey("a"), "right")).toEqual({});
    });
});

describe("formatting", () => {
    const now = 10_000_000_000;
    it("formats ages", () => {
        expect(formatAge(now - 5_000, now)).toBe("now");
        expect(formatAge(now - 5 * 60_000, now)).toBe("5m");
        expect(formatAge(now - 3 * 3_600_000, now)).toBe("3h");
        expect(formatAge(now - 2 * 86_400_000, now)).toBe("2d");
        expect(formatAge(now - 90 * 86_400_000, now)).toBe("3mo");
        expect(formatAge(0, now)).toBe("");
    });

    it("shortens the home folder only at a path boundary", () => {
        expect(shortenPath("/home/u/p", "/home/u")).toBe("~/p");
        expect(shortenPath("/home/u", "/home/u")).toBe("~");
        expect(shortenPath("/home/ux/p", "/home/u")).toBe("/home/ux/p");
        expect(shortenPath("", "/home/u")).toBe("(unknown folder)");
    });

    it("shows the id when a session has no name", () => {
        expect(displayName(mk("abc", "/", 1))).toBe("abc");
        expect(displayName(mk("abc", "/", 1, { name: "n" }))).toBe("n");
    });
});

describe("remembered folders", () => {
    const folders = [{ path: "/p/empty" }, { path: "/p/one" }];

    it("lists a remembered folder with no sessions, in alphabetical order with the rest", () => {
        const rows = buildRows(sessions, { ...opts, folders });
        const groups = rows.filter((r) => r.kind === "group");
        expect(groups.map((g) => g.cwd)).toEqual(["/p/empty", "/p/one", "/p/two"]);
        expect(groups[0]).toMatchObject({ count: 0, remembered: true });
        expect(groups[1]).toMatchObject({ remembered: true });
        expect(groups[2]).toMatchObject({ remembered: false });
    });

    it("keeps an empty remembered folder when offline sessions are hidden", () => {
        const rows = buildRows(sessions, { ...opts, folders, showOffline: false });
        expect(rows.some((r) => r.key === groupKey("/p/empty"))).toBe(true);
    });

    it("only matches an empty remembered folder by path when filtering", () => {
        expect(buildRows(sessions, { ...opts, folders, filter: "empty" }).map((r) => r.key)).toEqual([
            groupKey("/p/empty"),
        ]);
        expect(
            buildRows(sessions, { ...opts, folders, filter: "alpha" }).some((r) => r.key === groupKey("/p/empty"))
        ).toBe(false);
    });

    it("flags missing folders", () => {
        const rows = buildRows(sessions, { ...opts, missing: ["/p/two"] });
        expect(rows.find((r) => r.key === groupKey("/p/two"))).toMatchObject({ missing: true });
        expect(rows.find((r) => r.key === groupKey("/p/one"))).toMatchObject({ missing: false });
    });
});

describe("removed sessions", () => {
    const withHidden = [
        ...sessions,
        mk("h", "/p/one", 10, { hidden: true }),
        mk("r", "/p/one", 400, { hidden: true, state: "idle", pid: 9 }),
    ];

    it("hides an offline removed session unless asked to show them", () => {
        const keys = (showHidden: boolean) => buildRows(withHidden, { ...opts, showHidden }).map((r) => r.key);
        expect(keys(false)).not.toContain(sessionKey("h"));
        expect(keys(true)).toContain(sessionKey("h"));
    });

    it("always shows a removed session that is running again", () => {
        expect(buildRows(withHidden, opts).map((r) => r.key)).toContain(sessionKey("r"));
        expect(isHidden(withHidden[5])).toBe(false);
        expect(isHidden(withHidden[4])).toBe(true);
    });

    it("drops a folder whose sessions are all removed", () => {
        const rows = buildRows([mk("h", "/gone", 1, { hidden: true })], opts);
        expect(rows).toEqual([]);
    });
});

describe("launchKeyAction", () => {
    const both = { claude: true, agy: true };
    const pick = { cwd: "/p", sessionId: null, label: "p", harness: null };
    const mode = { ...pick, harness: "claude" };

    it("picks a tool by key and ignores a missing one", () => {
        expect(launchKeyAction(pick, "c", both)).toEqual({ type: "tool", harness: "claude" });
        expect(launchKeyAction(pick, "A", both)).toEqual({ type: "tool", harness: "agy" });
        expect(launchKeyAction(pick, "a", { claude: true, agy: false })).toEqual({ type: "none" });
    });

    it("Enter takes the first tool that is available", () => {
        expect(launchKeyAction(pick, "Enter", { claude: false, agy: true })).toEqual({ type: "tool", harness: "agy" });
        expect(launchKeyAction(pick, "Enter", { claude: false, agy: false })).toEqual({ type: "none" });
    });

    it("asks normal or skip permissions; skip is never the default", () => {
        expect(launchKeyAction(mode, "Enter", both)).toEqual({ type: "launch", skipPermissions: false });
        expect(launchKeyAction(mode, "n", both)).toEqual({ type: "launch", skipPermissions: false });
        expect(launchKeyAction(mode, "s", both)).toEqual({ type: "launch", skipPermissions: true });
        expect(launchKeyAction(mode, "c", both)).toEqual({ type: "none" });
    });

    it("Escape cancels in both steps", () => {
        expect(launchKeyAction(pick, "Escape", both)).toEqual({ type: "cancel" });
        expect(launchKeyAction(mode, "Escape", both)).toEqual({ type: "cancel" });
    });
});
