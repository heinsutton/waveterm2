// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

export type SessionState = "busy" | "idle" | "waiting" | "external" | "offline";

export type GroupRow = {
    kind: "group";
    key: string;
    cwd: string;
    count: number;
    running: number;
    collapsed: boolean;
    remembered: boolean; // a folder the user asked to remember
    missing: boolean; // the folder no longer exists
};

export type SessionRow = {
    kind: "session";
    key: string;
    cwd: string;
    session: ClaudeSession;
    state: SessionState;
};

export type Row = GroupRow | SessionRow;

export type BuildOpts = {
    collapsed: Set<string>;
    filter: string;
    showOffline: boolean;
    showHidden?: boolean;
    descriptions: { [key: string]: string };
    folders?: ClaudeFolder[];
    missing?: string[];
};

export const UnknownFolder = "";

export function groupKey(cwd: string): string {
    return "g:" + cwd;
}

export function sessionKey(sessionId: string): string {
    return "s:" + sessionId;
}

// A session that is alive but was not started in a Bifrost pane is "external": it runs, but this
// app cannot follow its state, and it must not be resumed.
export function sessionState(s: ClaudeSession): SessionState {
    if (s.external && s.state !== "offline") {
        return "external";
    }
    if (s.state === "busy" || s.state === "idle" || s.state === "waiting" || s.state === "offline") {
        return s.state;
    }
    return "offline";
}

function matchesFilter(s: ClaudeSession, filter: string, descriptions: { [key: string]: string }): boolean {
    const hay = [s.name, s.sessionid, s.cwd, s.preview, descriptions?.[s.sessionid]].join("\n").toLowerCase();
    return hay.includes(filter);
}

// Groups are ordered alphabetically by path, sessions by running first, then recency.
export function buildRows(sessions: ClaudeSession[], opts: BuildOpts): Row[] {
    const filter = opts.filter.trim().toLowerCase();
    const groups = new Map<string, SessionRow[]>();
    for (const s of sessions ?? []) {
        const state = sessionState(s);
        if (state === "offline" && !opts.showOffline) {
            continue;
        }
        // Removed from the list, but a session that is running again always shows.
        if (s.hidden && state === "offline" && !opts.showHidden) {
            continue;
        }
        if (filter !== "" && !matchesFilter(s, filter, opts.descriptions)) {
            continue;
        }
        const cwd = s.cwd ?? UnknownFolder;
        if (!groups.has(cwd)) {
            groups.set(cwd, []);
        }
        groups.get(cwd).push({ kind: "session", key: sessionKey(s.sessionid), cwd, session: s, state });
    }
    // Remembered folders are listed even with no sessions; a filter only keeps those it matches by path.
    const remembered = new Set((opts.folders ?? []).map((f) => f.path));
    for (const f of opts.folders ?? []) {
        if (!groups.has(f.path) && (filter === "" || f.path.toLowerCase().includes(filter))) {
            groups.set(f.path, []);
        }
    }
    const missingSet = new Set(opts.missing ?? []);
    const ordered = [...groups.entries()].map(([cwd, rows]) => {
        const sorted = [...rows].sort((a, b) => {
            const ra = a.state === "offline" ? 1 : 0;
            const rb = b.state === "offline" ? 1 : 0;
            if (ra !== rb) {
                return ra - rb;
            }
            return b.session.lastactive - a.session.lastactive;
        });
        return { cwd, rows: sorted };
    });
    // Alphabetical by path so folders never move when sessions start, stop or get new activity; the
    // folder with no known path goes last.
    ordered.sort((a, b) => {
        if ((a.cwd === UnknownFolder) !== (b.cwd === UnknownFolder)) {
            return a.cwd === UnknownFolder ? 1 : -1;
        }
        const la = a.cwd.toLowerCase();
        const lb = b.cwd.toLowerCase();
        return la < lb ? -1 : la > lb ? 1 : a.cwd < b.cwd ? -1 : a.cwd > b.cwd ? 1 : 0;
    });
    const out: Row[] = [];
    for (const g of ordered) {
        // A filter opens every group so matches are never hidden.
        const collapsed = filter === "" && opts.collapsed.has(g.cwd);
        out.push({
            kind: "group",
            key: groupKey(g.cwd),
            cwd: g.cwd,
            count: g.rows.length,
            running: g.rows.filter((r) => r.state !== "offline" && r.state !== "external").length,
            collapsed,
            remembered: remembered.has(g.cwd),
            missing: missingSet.has(g.cwd),
        });
        if (!collapsed) {
            out.push(...g.rows);
        }
    }
    return out;
}

export function indexOfKey(rows: Row[], key: string): number {
    return rows.findIndex((r) => r.key === key);
}

// Moves the selection by delta rows, clamped; a missing selection lands on the first row.
export function moveSelection(rows: Row[], selectedKey: string, delta: number): string {
    if (rows.length === 0) {
        return null;
    }
    const idx = indexOfKey(rows, selectedKey);
    if (idx < 0) {
        return rows[0].key;
    }
    return rows[Math.min(rows.length - 1, Math.max(0, idx + delta))].key;
}

export function edgeSelection(rows: Row[], last: boolean): string {
    if (rows.length === 0) {
        return null;
    }
    return rows[last ? rows.length - 1 : 0].key;
}

export type FoldAction = { select?: string; toggle?: string };

// Left: a session goes to its folder, an open folder collapses. Right: a closed folder opens, an
// open folder goes to its first session.
export function foldAction(rows: Row[], selectedKey: string, dir: "left" | "right"): FoldAction {
    const idx = indexOfKey(rows, selectedKey);
    if (idx < 0) {
        return {};
    }
    const row = rows[idx];
    if (dir === "left") {
        if (row.kind === "session") {
            return { select: groupKey(row.cwd) };
        }
        return row.collapsed ? {} : { toggle: row.cwd };
    }
    if (row.kind === "group") {
        if (row.collapsed) {
            return { toggle: row.cwd };
        }
        const next = rows[idx + 1];
        return next != null && next.kind === "session" ? { select: next.key } : {};
    }
    return {};
}

export function formatAge(ms: number, now: number): string {
    if (!ms) {
        return "";
    }
    const sec = Math.max(0, Math.floor((now - ms) / 1000));
    if (sec < 60) {
        return "now";
    }
    const min = Math.floor(sec / 60);
    if (min < 60) {
        return `${min}m`;
    }
    const hr = Math.floor(min / 60);
    if (hr < 24) {
        return `${hr}h`;
    }
    const day = Math.floor(hr / 24);
    if (day < 60) {
        return `${day}d`;
    }
    return `${Math.floor(day / 30)}mo`;
}

export function shortenPath(cwd: string, home: string): string {
    if (cwd === UnknownFolder) {
        return "(unknown folder)";
    }
    if (home && (cwd === home || cwd.startsWith(home + "/") || cwd.startsWith(home + "\\"))) {
        return "~" + cwd.slice(home.length);
    }
    return cwd;
}

export function isHidden(s: ClaudeSession): boolean {
    return !!s.hidden && sessionState(s) === "offline";
}

export function displayName(s: ClaudeSession): string {
    return s.name ? s.name : s.sessionid;
}

// The tools a new session can start, in modal order. key is the letter that picks it.
export const HarnessChoices: { name: string; label: string; key: string; canName: boolean }[] = [
    { name: "claude", label: "Claude", key: "c", canName: true },
    { name: "agy", label: "Antigravity (agy)", key: "a", canName: false }, // agy cannot be named at startup
];

export function harnessChoiceLabel(name: string): string {
    return HarnessChoices.find((c) => c.name === name)?.label ?? name;
}

// What the launch modal is for: a new session in a folder, or resuming one (tool already known).
export type LaunchPrompt = {
    cwd: string;
    sessionId: string | null; // set: resume this session; null: start a new one
    label: string;
    harness: string | null; // resume: the session's tool
};

export type LaunchChoice = { harness: string; name: string; skipPermissions: boolean };

// The first tool that can be started, for the modal's default selection.
export function defaultHarness(available: { [harness: string]: boolean }): string {
    return (HarnessChoices.find((c) => available[c.name]) ?? HarnessChoices[0]).name;
}

// The name actually sent: empty when the tool cannot be named at startup or nothing was typed.
export function launchName(harness: string, typed: string): string {
    const choice = HarnessChoices.find((c) => c.name === harness);
    return choice?.canName ? typed.trim() : "";
}
