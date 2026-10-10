// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { createBlockSplitHorizontally } from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import type { WaveEnv, WaveEnvSubset } from "@/app/waveenv/waveenv";
import { fireAndForget } from "@/util/util";
import * as jotai from "jotai";
import * as React from "react";
import { ClaudeSessionsView } from "./claudesessions";
import {
    buildRows,
    edgeSelection,
    foldAction,
    groupKey,
    indexOfKey,
    isHidden,
    moveSelection,
    Row,
    sessionKey,
    sessionState,
} from "./claudesessions-nav";

type ClaudeSessionsEnv = WaveEnvSubset<{
    rpc: {
        ClaudeSessionsListCommand: WaveEnv["rpc"]["ClaudeSessionsListCommand"];
        ClaudeSessionsPrepareCommand: WaveEnv["rpc"]["ClaudeSessionsPrepareCommand"];
        ClaudeSessionsAddFolderCommand: WaveEnv["rpc"]["ClaudeSessionsAddFolderCommand"];
        ClaudeSessionsRemoveFolderCommand: WaveEnv["rpc"]["ClaudeSessionsRemoveFolderCommand"];
        ClaudeSessionsSetDescriptionCommand: WaveEnv["rpc"]["ClaudeSessionsSetDescriptionCommand"];
        ClaudeSessionsSetHiddenCommand: WaveEnv["rpc"]["ClaudeSessionsSetHiddenCommand"];
        ClaudeSessionsPromptsCommand: WaveEnv["rpc"]["ClaudeSessionsPromptsCommand"];
    };
}>;

const PollIntervalMs = 3000;
const DefaultPageSize = 10;
const MessageMs = 5000;

const ShowOfflineStorageKey = "claudesessions:showoffline";
const PromptLimit = 5;

export type StatusMessage = { text: string; isError: boolean };

export type DescriptionEdit = { sessionId: string; name: string };

export type PromptsEntry = { prompts: ClaudePrompt[]; lastactive: number; error?: string };

function loadShowOffline(): boolean {
    try {
        return localStorage.getItem(ShowOfflineStorageKey) !== "false";
    } catch (_) {
        return true;
    }
}

function saveShowOffline(show: boolean) {
    try {
        localStorage.setItem(ShowOfflineStorageKey, String(show));
    } catch (_) {
        // storage can be unavailable; the toggle then just resets next time
    }
}

function isPlain(e: WaveKeyboardEvent, key: string): boolean {
    return e.key === key && !e.control && !e.alt && !e.cmd && !e.meta && !e.option;
}

export class ClaudeSessionsViewModel implements ViewModel {
    viewType = "claudesessions";
    blockId: string;
    env: ClaudeSessionsEnv;

    viewIcon = jotai.atom<string>("robot");
    viewName = jotai.atom<string>("Claude Sessions");
    noPadding = jotai.atom<boolean>(true);

    dataAtom = jotai.atom<ClaudeListResult>(null) as jotai.PrimitiveAtom<ClaudeListResult>;
    errorAtom = jotai.atom<string>(null) as jotai.PrimitiveAtom<string>;
    selectedKeyAtom = jotai.atom<string>(null) as jotai.PrimitiveAtom<string>;
    collapsedAtom = jotai.atom<Set<string>>(new Set<string>());
    filterAtom = jotai.atom<string>("");
    filterOpenAtom = jotai.atom<boolean>(false);
    showOfflineAtom = jotai.atom<boolean>(loadShowOffline());
    promptsOpenAtom = jotai.atom<boolean>(false);
    showHiddenAtom = jotai.atom<boolean>(false);
    editAtom = jotai.atom<DescriptionEdit>(null) as jotai.PrimitiveAtom<DescriptionEdit>;
    editValueAtom = jotai.atom<string>("");
    promptsAtom = jotai.atom<{ [sessionId: string]: PromptsEntry }>({});
    helpOpenAtom = jotai.atom<boolean>(false);
    addOpenAtom = jotai.atom<boolean>(false);
    addValueAtom = jotai.atom<string>("");
    messageAtom = jotai.atom<StatusMessage>(null) as jotai.PrimitiveAtom<StatusMessage>;
    rowsAtom: jotai.Atom<Row[]>;

    containerRef = React.createRef<HTMLDivElement>();
    filterInputRef = React.createRef<HTMLInputElement>();
    addInputRef = React.createRef<HTMLInputElement>();
    editInputRef = React.createRef<HTMLInputElement>();
    messageTimer: ReturnType<typeof setTimeout> | null = null;
    pageSize = DefaultPageSize;
    disposed = false;
    pollTimer: ReturnType<typeof setTimeout> | null = null;

    constructor({ blockId, waveEnv }: ViewModelInitType) {
        this.blockId = blockId;
        this.env = waveEnv;
        this.rowsAtom = jotai.atom((get) =>
            buildRows(get(this.dataAtom)?.sessions ?? [], {
                collapsed: get(this.collapsedAtom),
                filter: get(this.filterAtom),
                showOffline: get(this.showOfflineAtom),
                showHidden: get(this.showHiddenAtom),
                descriptions: get(this.dataAtom)?.descriptions ?? {},
                folders: get(this.dataAtom)?.folders ?? [],
                missing: get(this.dataAtom)?.missing ?? [],
            })
        );
        this.poll();
    }

    get viewComponent(): ViewComponent {
        return ClaudeSessionsView;
    }

    showMessage(text: string, isError: boolean) {
        globalStore.set(this.messageAtom, { text, isError });
        if (this.messageTimer != null) {
            clearTimeout(this.messageTimer);
        }
        this.messageTimer = setTimeout(() => globalStore.set(this.messageAtom, null), MessageMs);
    }

    async refresh() {
        try {
            globalStore.set(this.dataAtom, await this.env.rpc.ClaudeSessionsListCommand(TabRpcClient));
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    // The backend re-checks everything right before launch (session not running, folder exists,
    // claude found), so the pane only turns its answer into a split pane beside this one.
    async launch(data: CommandClaudeSessionsPrepareData, what: string) {
        try {
            const launch = await this.env.rpc.ClaudeSessionsPrepareCommand(TabRpcClient, data);
            await createBlockSplitHorizontally(
                {
                    meta: {
                        view: "term",
                        controller: "cmd",
                        cmd: launch.cmd,
                        "cmd:args": launch.args ?? [],
                        "cmd:shell": false,
                        "cmd:jwt": true,
                        "cmd:cwd": launch.cwd,
                        "cmd:runonstart": true,
                        "cmd:runonce": true,
                        "cmd:clearonstart": true,
                    },
                },
                this.blockId,
                "after"
            );
            this.showMessage(`${what} opened`, false);
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    resumeSession(s: ClaudeSession) {
        const state = sessionState(s);
        if (state === "external") {
            this.showMessage("Already running outside Bifrost, so it can't be resumed here", true);
        } else if (state !== "offline") {
            this.showMessage("Already running in Bifrost", true);
        } else {
            fireAndForget(() => this.launch({ harness: s.harness, sessionid: s.sessionid }, "Resume"));
        }
    }

    newSessionIn(cwd: string) {
        if (cwd === "") {
            this.showMessage("This session has no known folder", true);
            return;
        }
        fireAndForget(() => this.launch({ cwd }, "New session"));
    }

    openAdd() {
        globalStore.set(this.addValueAtom, "");
        globalStore.set(this.addOpenAtom, true);
        setTimeout(() => this.addInputRef.current?.focus(), 0);
    }

    closeAdd() {
        globalStore.set(this.addOpenAtom, false);
        this.giveFocus();
    }

    async submitAdd() {
        const path = globalStore.get(this.addValueAtom).trim();
        if (path === "") {
            return;
        }
        try {
            const stored = await this.env.rpc.ClaudeSessionsAddFolderCommand(TabRpcClient, { path });
            this.closeAdd();
            this.showMessage(`Remembered ${stored}`, false);
            await this.refresh();
            this.select(groupKey(stored));
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    async removeFolder(cwd: string) {
        try {
            await this.env.rpc.ClaudeSessionsRemoveFolderCommand(TabRpcClient, { path: cwd });
            this.showMessage("Folder forgotten (nothing was deleted)", false);
            await this.refresh();
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    openEdit(s: ClaudeSession) {
        const current = globalStore.get(this.dataAtom)?.descriptions?.[s.sessionid] ?? "";
        globalStore.set(this.editValueAtom, current);
        globalStore.set(this.editAtom, { sessionId: s.sessionid, name: s.name || s.sessionid });
        setTimeout(() => this.editInputRef.current?.focus(), 0);
    }

    closeEdit() {
        globalStore.set(this.editAtom, null);
        this.giveFocus();
    }

    async submitEdit() {
        const edit = globalStore.get(this.editAtom);
        if (edit == null) {
            return;
        }
        try {
            await this.env.rpc.ClaudeSessionsSetDescriptionCommand(TabRpcClient, {
                sessionid: edit.sessionId,
                description: globalStore.get(this.editValueAtom),
            });
            this.closeEdit();
            await this.refresh();
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    editSelected() {
        const row = this.selectedRow();
        if (row?.kind === "session") {
            this.openEdit(row.session);
        } else {
            this.showMessage("Select a session to describe", true);
        }
    }

    // Prompts come from a small history file, so one fetch per selection is cheap; a session with new
    // activity is fetched again.
    async loadPrompts(s: ClaudeSession) {
        const cached = globalStore.get(this.promptsAtom)[s.sessionid];
        if (cached != null && cached.lastactive === s.lastactive) {
            return;
        }
        let entry: PromptsEntry;
        try {
            const prompts = await this.env.rpc.ClaudeSessionsPromptsCommand(TabRpcClient, {
                harness: s.harness,
                sessionid: s.sessionid,
                limit: PromptLimit,
            });
            entry = { prompts: prompts ?? [], lastactive: s.lastactive };
        } catch (e) {
            entry = { prompts: [], lastactive: s.lastactive, error: String(e) };
        }
        if (!this.disposed) {
            globalStore.set(this.promptsAtom, { ...globalStore.get(this.promptsAtom), [s.sessionid]: entry });
        }
    }

    toggleShowHidden() {
        globalStore.set(this.showHiddenAtom, !globalStore.get(this.showHiddenAtom));
    }

    // "Delete" only removes the entry from this list (it can be brought back); Claude's own session
    // files are never touched, and a running session cannot be removed.
    async setHidden(s: ClaudeSession, hidden: boolean) {
        if (hidden && sessionState(s) !== "offline") {
            this.showMessage("A running session can't be removed from the list — close it first", true);
            return;
        }
        if (hidden && !globalStore.get(this.showHiddenAtom)) {
            const rows = globalStore.get(this.rowsAtom);
            const idx = indexOfKey(rows, sessionKey(s.sessionid));
            const neighbour = rows[idx + 1] ?? rows[idx - 1];
            if (neighbour != null) {
                this.select(neighbour.key);
            }
        }
        try {
            await this.env.rpc.ClaudeSessionsSetHiddenCommand(TabRpcClient, { sessionid: s.sessionid, hidden });
            this.showMessage(
                hidden
                    ? "Removed from the list (files untouched) — press H to show removed sessions"
                    : "Back in the list",
                false
            );
            await this.refresh();
        } catch (e) {
            this.showMessage(String(e), true);
        }
    }

    hideSelected() {
        const row = this.selectedRow();
        if (row?.kind !== "session") {
            this.showMessage("Select a session to remove from the list", true);
            return;
        }
        fireAndForget(() => this.setHidden(row.session, !isHidden(row.session)));
    }

    togglePrompts() {
        globalStore.set(this.promptsOpenAtom, !globalStore.get(this.promptsOpenAtom));
    }

    async poll() {
        if (this.disposed) {
            return;
        }
        try {
            const data = await this.env.rpc.ClaudeSessionsListCommand(TabRpcClient);
            if (!this.disposed) {
                globalStore.set(this.dataAtom, data);
                globalStore.set(this.errorAtom, null);
            }
        } catch (e) {
            if (!this.disposed) {
                globalStore.set(this.errorAtom, String(e));
            }
        }
        if (!this.disposed) {
            this.pollTimer = setTimeout(() => this.poll(), PollIntervalMs);
        }
    }

    giveFocus(): boolean {
        if (globalStore.get(this.editAtom) != null && this.editInputRef.current != null) {
            this.editInputRef.current.focus();
            return true;
        }
        if (globalStore.get(this.addOpenAtom) && this.addInputRef.current != null) {
            this.addInputRef.current.focus();
            return true;
        }
        if (globalStore.get(this.filterOpenAtom) && this.filterInputRef.current != null) {
            this.filterInputRef.current.focus();
            return true;
        }
        if (this.containerRef.current == null) {
            return false;
        }
        this.containerRef.current.focus({ preventScroll: true });
        return true;
    }

    select(key: string) {
        if (key != null) {
            globalStore.set(this.selectedKeyAtom, key);
        }
    }

    // The selection can vanish when a session disappears or a filter hides it; it then falls back
    // to the first row so the keyboard always has a starting point.
    currentKey(): string {
        const rows = globalStore.get(this.rowsAtom);
        const key = globalStore.get(this.selectedKeyAtom);
        return indexOfKey(rows, key) >= 0 ? key : null;
    }

    move(delta: number) {
        this.select(moveSelection(globalStore.get(this.rowsAtom), this.currentKey(), delta));
    }

    toggleGroup(cwd: string) {
        const next = new Set(globalStore.get(this.collapsedAtom));
        if (next.has(cwd)) {
            next.delete(cwd);
        } else {
            next.add(cwd);
        }
        globalStore.set(this.collapsedAtom, next);
        this.select(groupKey(cwd));
    }

    fold(dir: "left" | "right") {
        const key = this.currentKey();
        if (key == null) {
            return;
        }
        const action = foldAction(globalStore.get(this.rowsAtom), key, dir);
        if (action.toggle != null) {
            this.toggleGroup(action.toggle);
        }
        if (action.select != null) {
            this.select(action.select);
        }
    }

    selectedRow(): Row {
        const rows = globalStore.get(this.rowsAtom);
        return rows[indexOfKey(rows, this.currentKey())];
    }

    activate() {
        const row = this.selectedRow();
        if (row?.kind === "group") {
            this.toggleGroup(row.cwd);
        } else if (row?.kind === "session") {
            this.resumeSession(row.session);
        }
    }

    newInSelected() {
        const row = this.selectedRow();
        if (row != null) {
            this.newSessionIn(row.cwd);
        }
    }

    removeSelectedFolder() {
        const row = this.selectedRow();
        if (row?.kind !== "group") {
            return;
        }
        if (!row.remembered) {
            this.showMessage("Only remembered folders can be forgotten; this one comes from its sessions", true);
            return;
        }
        fireAndForget(() => this.removeFolder(row.cwd));
    }

    openFilter() {
        globalStore.set(this.filterOpenAtom, true);
        setTimeout(() => this.filterInputRef.current?.focus(), 0);
    }

    closeFilter(clear: boolean) {
        if (clear) {
            globalStore.set(this.filterAtom, "");
        }
        globalStore.set(this.filterOpenAtom, globalStore.get(this.filterAtom) !== "");
        this.giveFocus();
    }

    toggleOffline() {
        const next = !globalStore.get(this.showOfflineAtom);
        globalStore.set(this.showOfflineAtom, next);
        saveShowOffline(next);
    }

    toggleHelp() {
        globalStore.set(this.helpOpenAtom, !globalStore.get(this.helpOpenAtom));
    }

    keyDownHandler(e: WaveKeyboardEvent): boolean {
        if (e.control || e.alt || e.cmd || e.meta || e.option) {
            return false;
        }
        if (globalStore.get(this.helpOpenAtom)) {
            if (e.key === "Escape" || e.key === "?" || e.key === "Enter") {
                this.toggleHelp();
                return true;
            }
            return false;
        }
        const rows = globalStore.get(this.rowsAtom);
        switch (e.key) {
            case "ArrowDown":
            case "j":
                this.move(1);
                return true;
            case "ArrowUp":
            case "k":
                this.move(-1);
                return true;
            case "PageDown":
                this.move(this.pageSize);
                return true;
            case "PageUp":
                this.move(-this.pageSize);
                return true;
            case "Home":
            case "g":
                this.select(edgeSelection(rows, false));
                return true;
            case "End":
            case "G":
                this.select(edgeSelection(rows, true));
                return true;
            case "ArrowLeft":
            case "h":
                this.fold("left");
                return true;
            case "ArrowRight":
            case "l":
                this.fold("right");
                return true;
            case "Enter":
            case " ":
                this.activate();
                return true;
            case "Escape":
                if (globalStore.get(this.filterAtom) !== "") {
                    this.closeFilter(true);
                    return true;
                }
                return false;
        }
        if (isPlain(e, "/")) {
            this.openFilter();
            return true;
        }
        if (isPlain(e, "o")) {
            this.toggleOffline();
            return true;
        }
        if (isPlain(e, "n")) {
            this.newInSelected();
            return true;
        }
        if (isPlain(e, "d") || e.key === "Delete") {
            this.hideSelected();
            return true;
        }
        if (isPlain(e, "H")) {
            this.toggleShowHidden();
            return true;
        }
        if (isPlain(e, "e")) {
            this.editSelected();
            return true;
        }
        if (isPlain(e, "p")) {
            this.togglePrompts();
            return true;
        }
        if (isPlain(e, "a")) {
            this.openAdd();
            return true;
        }
        if (isPlain(e, "x")) {
            this.removeSelectedFolder();
            return true;
        }
        if (isPlain(e, "?")) {
            this.toggleHelp();
            return true;
        }
        return false;
    }

    dispose() {
        this.disposed = true;
        if (this.pollTimer != null) {
            clearTimeout(this.pollTimer);
            this.pollTimer = null;
        }
        if (this.messageTimer != null) {
            clearTimeout(this.messageTimer);
            this.messageTimer = null;
        }
    }
}
