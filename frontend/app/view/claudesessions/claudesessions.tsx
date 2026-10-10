// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ContextMenuModel } from "@/app/store/contextmenu";
import { getApi } from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { cn } from "@/shadcn/lib/utils";
import { fireAndForget } from "@/util/util";
import * as jotai from "jotai";
import * as React from "react";
import { ClaudeSessionsViewModel, PromptsEntry } from "./claudesessions-model";
import {
    displayName,
    formatAge,
    isHidden,
    Row,
    SessionRow,
    SessionState,
    sessionState,
    shortenPath,
} from "./claudesessions-nav";

const RowHeight = 22;
const RuleFill = "─".repeat(300);

type StateStyle = { glyph: string; className: string; label: string; word: string; pulse?: boolean };

// `word` is the short label shown in every row.
const StateGlyph: Record<SessionState, StateStyle> = {
    busy: { glyph: "●", className: "text-success", label: "busy", word: "busy", pulse: true },
    idle: { glyph: "○", className: "text-accent", label: "idle", word: "idle" },
    waiting: { glyph: "◆", className: "text-attention", label: "waiting for input", word: "waiting", pulse: true },
    external: { glyph: "◌", className: "text-warning", label: "running outside Bifrost", word: "outside" },
    offline: { glyph: "·", className: "text-muted", label: "offline", word: "offline" },
};

// Which tool a session belongs to; new harnesses plug in here.
// the selected row must beat the harness tints: stronger fill, accent bar and an inset outline
const SelectedRowClass = "bg-accent/35 border-accent ring-1 ring-inset ring-accent";

type HarnessLook = { label: string; className: string; rowClassName: string };

// rowClassName is a faint background so the tool behind a row is clear at a glance
const HarnessStyle: { [harness: string]: HarnessLook } = {
    claude: {
        label: "claude",
        className: "text-[#d97757]",
        rowClassName: "bg-[#d97757]/[0.2] hover:bg-[#d97757]/[0.3]",
    },
    agy: { label: "agy", className: "text-[#4285f4]", rowClassName: "bg-[#4285f4]/[0.22] hover:bg-[#4285f4]/[0.32]" },
};

function harnessStyle(harness: string): HarnessLook {
    return (
        HarnessStyle[harness] ?? {
            label: harness || "?",
            className: "text-muted-foreground",
            rowClassName: "hover:bg-hover",
        }
    );
}

const HelpKeys: [string, string][] = [
    ["↑ ↓  j k", "move"],
    ["← →  h l", "close / open folder, jump to folder"],
    ["PgUp PgDn", "move a page"],
    ["Home End  g G", "first / last"],
    ["Enter  Space", "folder: open / close · session: resume it"],
    ["n", "new session in the selected folder"],
    ["e", "edit the description of the selected session"],
    ["p", "open / close the recent prompts box"],
    ["a", "add (remember) a folder"],
    ["x", "forget a remembered folder"],
    ["d  Delete", "remove the selected offline session from the list (files untouched)"],
    ["H", "show / hide removed sessions (d on one puts it back)"],
    ["/", "filter (Esc clears)"],
    ["o", "show / hide offline sessions"],
    ["?", "this help"],
    ["mouse", "click selects · double-click resumes · click a folder to fold · right-click for actions"],
];

function getHome(): string {
    try {
        return getApi().getEnv("HOME") || getApi().getEnv("USERPROFILE") || "";
    } catch (_) {
        return "";
    }
}

type RowProps = {
    row: Row;
    selected: boolean;
    last: boolean;
    now: number;
    home: string;
    description: string;
    model: ClaudeSessionsViewModel;
};

const GroupLine = React.memo(({ row, selected, home, model }: RowProps & { row: Extract<Row, { kind: "group" }> }) => {
    return (
        <div
            data-rowkey={row.key}
            className={cn(
                "flex items-center gap-2 px-2 cursor-pointer border-l-2 whitespace-nowrap",
                selected ? SelectedRowClass : "border-transparent hover:bg-hover"
            )}
            style={{ height: RowHeight }}
            onClick={() => {
                model.containerRef.current?.focus({ preventScroll: true });
                model.toggleGroup(row.cwd);
            }}
            onContextMenu={(e) => {
                e.preventDefault();
                model.select(row.key);
                const menu: ContextMenuItem[] = [
                    {
                        label: "New session here",
                        enabled: row.cwd !== "" && !row.missing,
                        click: () => model.newSessionIn(row.cwd),
                    },
                    { label: "Copy folder", click: () => navigator.clipboard.writeText(row.cwd) },
                    {
                        label: row.collapsed ? "Open folder group" : "Close folder group",
                        click: () => model.toggleGroup(row.cwd),
                    },
                    { type: "separator" },
                    { label: "Add folder…", click: () => model.openAdd() },
                    {
                        label: "Forget remembered folder",
                        enabled: row.remembered,
                        click: () => model.removeSelectedFolder(),
                    },
                ];
                ContextMenuModel.getInstance().showContextMenu(menu, e);
            }}
        >
            <span className="text-accent w-[1.5ch] shrink-0 text-center">{row.collapsed ? "▸" : "▾"}</span>
            <span
                className={cn(
                    "shrink-0 max-w-[60%] truncate",
                    selected ? "text-accenthover" : "text-accent",
                    row.missing && "text-muted line-through"
                )}
                title={row.missing ? "folder no longer exists" : row.cwd}
            >
                {shortenPath(row.cwd, home)}
            </span>
            {row.missing ? <span className="text-attention shrink-0">missing</span> : null}
            {row.remembered ? (
                <span className="text-muted-foreground shrink-0" title="remembered folder">
                    ★
                </span>
            ) : null}
            <span className="flex-1 overflow-hidden text-border select-none" aria-hidden="true">
                {RuleFill}
            </span>
            <span className="text-muted-foreground shrink-0">
                {row.count}
                {row.running > 0 ? <span className="text-success"> · {row.running} running</span> : null}
            </span>
        </div>
    );
});
GroupLine.displayName = "GroupLine";

const SessionLine = React.memo(({ row, selected, last, now, description, model }: RowProps & { row: SessionRow }) => {
    const s = row.session;
    const st = StateGlyph[row.state];
    const harness = harnessStyle(s.harness);
    const hidden = isHidden(s);
    const hasName = !!s.name;
    return (
        <div
            data-rowkey={row.key}
            className={cn(
                "flex items-center gap-2 px-2 cursor-pointer border-l-2 whitespace-nowrap",
                selected ? SelectedRowClass : cn("border-transparent", harness.rowClassName),
                hidden && "opacity-50"
            )}
            style={{ height: RowHeight }}
            title={hidden ? "removed from the list — press d to put it back" : undefined}
            onClick={() => {
                model.containerRef.current?.focus({ preventScroll: true });
                model.select(row.key);
            }}
            onDoubleClick={() => model.resumeSession(s)}
            onContextMenu={(e) => {
                e.preventDefault();
                model.select(row.key);
                const menu: ContextMenuItem[] = [
                    {
                        label: "Resume",
                        enabled: row.state === "offline",
                        click: () => model.resumeSession(s),
                    },
                    {
                        label: "New session in this folder",
                        enabled: row.cwd !== "",
                        click: () => model.newSessionIn(row.cwd),
                    },
                    { label: "Edit description…", click: () => model.openEdit(s) },
                    {
                        label: hidden ? "Put back in the list" : "Remove from the list",
                        enabled: hidden || row.state === "offline",
                        click: () => fireAndForget(() => model.setHidden(s, !hidden)),
                    },
                    { type: "separator" },
                    { label: "Copy session ID", click: () => navigator.clipboard.writeText(s.sessionid) },
                    { label: "Copy folder", click: () => navigator.clipboard.writeText(row.cwd) },
                ];
                ContextMenuModel.getInstance().showContextMenu(menu, e);
            }}
        >
            <span className="text-border w-[1.5ch] shrink-0 text-center select-none">{last ? "└" : "├"}</span>
            <span
                className={cn(
                    "w-[1.5ch] shrink-0 text-center",
                    st.className,
                    st.pulse && "animate-pulse motion-reduce:animate-none"
                )}
                title={st.label}
            >
                {st.glyph}
            </span>
            <span
                className={cn(
                    "truncate",
                    "w-[min(24ch,40%)] shrink-0",
                    !hasName && "text-muted-foreground",
                    row.state === "offline" && hasName && "text-secondary"
                )}
                title={s.sessionid}
            >
                {displayName(s)}
            </span>
            <span className={cn("w-[10ch] shrink-0 truncate", st.className, st.pulse && "font-bold")} title={st.label}>
                {st.word}
            </span>
            <span className={cn("w-[7ch] shrink-0 truncate", harness.className)} title={`${harness.label} session`}>
                {harness.label}
            </span>
            <span className="w-[4ch] shrink-0 text-right text-muted-foreground">{formatAge(s.lastactive, now)}</span>
            {description ? (
                <span
                    className="flex min-w-0 flex-1 items-center gap-1.5"
                    title="your description — double-click to edit"
                    onDoubleClick={(e) => {
                        e.stopPropagation();
                        model.openEdit(s);
                    }}
                >
                    <span className="shrink-0 text-accent select-none">▌</span>
                    <span className="truncate font-bold text-accenthover">{description}</span>
                </span>
            ) : (
                <span
                    className="flex min-w-0 flex-1 items-center gap-1.5"
                    title="last prompt (press e to write a description instead)"
                >
                    <span className="shrink-0 text-muted select-none">›</span>
                    <span className="truncate italic text-muted">{s.preview}</span>
                </span>
            )}
        </div>
    );
});
SessionLine.displayName = "SessionLine";

type DetailProps = {
    row: Row;
    home: string;
    now: number;
    description: string;
    prompts: PromptsEntry;
    expanded: boolean;
    onTogglePrompts: () => void;
};

// Id line, description and the prompts toggle are always there; the prompts box adds PromptRows. The
// height depends only on whether the box is open, never on the selection.
const PromptRows = 5;
const ClosedRows = 3;

const DetailPanel = React.memo(({ row, home, now, description, prompts, expanded, onTogglePrompts }: DetailProps) => {
    const session = row?.kind === "session" ? row.session : null;
    return (
        <div
            className="min-h-0 shrink overflow-hidden"
            style={{ height: (expanded ? ClosedRows + PromptRows : ClosedRows) * RowHeight }}
        >
            <div className="px-2 text-muted-foreground truncate" style={{ height: RowHeight }}>
                {session != null ? (
                    <>
                        <span className="text-accent">{session.sessionid}</span>
                        {session.version ? <span> · v{session.version}</span> : null}
                        <span> · {shortenPath(row.cwd, home)}</span>
                    </>
                ) : row != null ? (
                    <span>{row.cwd === "" ? "(unknown folder)" : row.cwd}</span>
                ) : null}
            </div>
            {session != null ? (
                <>
                    <div
                        className={cn(
                            "flex items-center gap-2 px-2 border-l-2 whitespace-nowrap",
                            description ? "border-accent bg-accent/10" : "border-border bg-accent/5"
                        )}
                        style={{ height: RowHeight }}
                    >
                        <span className="shrink-0 text-accent text-[11px] tracking-widest select-none">▌NOTE</span>
                        {description ? (
                            <span className="min-w-0 flex-1 truncate font-bold text-accenthover" title={description}>
                                {description}
                            </span>
                        ) : (
                            <span className="min-w-0 flex-1 truncate italic text-muted">
                                nothing written yet — press e to describe this session
                            </span>
                        )}
                    </div>
                    <div
                        className="flex items-center gap-2 px-2 cursor-pointer whitespace-nowrap hover:bg-hover"
                        style={{ height: RowHeight }}
                        onClick={onTogglePrompts}
                        title="open / close (p)"
                    >
                        <span className="text-accent w-[1.5ch] shrink-0 text-center">{expanded ? "▾" : "▸"}</span>
                        {prompts?.error && expanded ? (
                            <span className="min-w-0 flex-1 truncate text-error">{prompts.error}</span>
                        ) : (
                            <span className="min-w-0 flex-1 truncate text-muted-foreground">
                                recent prompts
                                {expanded && prompts == null ? " …" : ""}
                                {expanded && prompts != null && prompts.prompts.length === 0 ? " — none recorded" : ""}
                                {!expanded ? " (p to open)" : ""}
                            </span>
                        )}
                    </div>
                    {expanded
                        ? (prompts?.prompts ?? []).slice(0, PromptRows).map((p, i) => (
                              <div key={i} className="flex gap-2 px-2 whitespace-nowrap" style={{ height: RowHeight }}>
                                  <span className="w-[4ch] shrink-0 text-right text-muted-foreground">
                                      {formatAge(p.ts, now)}
                                  </span>
                                  <span className="min-w-0 flex-1 truncate text-muted-foreground" title={p.text}>
                                      {p.text}
                                  </span>
                              </div>
                          ))
                        : null}
                </>
            ) : null}
        </div>
    );
});
DetailPanel.displayName = "DetailPanel";

export const ClaudeSessionsView: React.FC<ViewComponentProps<ClaudeSessionsViewModel>> = React.memo(
    function ClaudeSessionsView({ model }) {
        const data = jotai.useAtomValue(model.dataAtom);
        const error = jotai.useAtomValue(model.errorAtom);
        const rows = jotai.useAtomValue(model.rowsAtom);
        const selectedKey = jotai.useAtomValue(model.selectedKeyAtom);
        const filter = jotai.useAtomValue(model.filterAtom);
        const filterOpen = jotai.useAtomValue(model.filterOpenAtom);
        const showOffline = jotai.useAtomValue(model.showOfflineAtom);
        const helpOpen = jotai.useAtomValue(model.helpOpenAtom);
        const addOpen = jotai.useAtomValue(model.addOpenAtom);
        const addValue = jotai.useAtomValue(model.addValueAtom);
        const message = jotai.useAtomValue(model.messageAtom);
        const edit = jotai.useAtomValue(model.editAtom);
        const editValue = jotai.useAtomValue(model.editValueAtom);
        const prompts = jotai.useAtomValue(model.promptsAtom);
        const promptsOpen = jotai.useAtomValue(model.promptsOpenAtom);
        const listRef = React.useRef<HTMLDivElement>(null);
        const home = React.useMemo(getHome, []);
        const [now, setNow] = React.useState(() => Date.now());

        React.useEffect(() => {
            const timer = setInterval(() => setNow(Date.now()), 30000);
            return () => clearInterval(timer);
        }, []);

        const effectiveKey = rows.some((r) => r.key === selectedKey) ? selectedKey : (rows[0]?.key ?? null);
        React.useEffect(() => {
            if (effectiveKey != null && effectiveKey !== selectedKey) {
                globalStore.set(model.selectedKeyAtom, effectiveKey);
            }
        }, [effectiveKey, selectedKey]);

        React.useEffect(() => {
            if (effectiveKey == null || listRef.current == null) {
                return;
            }
            const el = listRef.current.querySelector(`[data-rowkey="${CSS.escape(effectiveKey)}"]`);
            el?.scrollIntoView({ block: "nearest" });
        }, [effectiveKey]);

        React.useEffect(() => {
            const el = listRef.current;
            if (el == null) {
                return;
            }
            const update = () => {
                model.pageSize = Math.max(1, Math.floor(el.clientHeight / RowHeight) - 1);
            };
            update();
            const ro = new ResizeObserver(update);
            ro.observe(el);
            return () => ro.disconnect();
        }, []);

        const sessions = data?.sessions ?? [];
        const running = sessions.filter((s) => ["busy", "idle", "waiting"].includes(sessionState(s))).length;
        const hiddenCount = sessions.filter(isHidden).length;
        const showHidden = jotai.useAtomValue(model.showHiddenAtom);
        const external = sessions.filter((s) => sessionState(s) === "external").length;
        const waiting = sessions.filter((s) => sessionState(s) === "waiting").length;
        const folderCount = new Set(sessions.map((s) => s.cwd ?? "")).size;
        const selectedRow = rows.find((r) => r.key === effectiveKey) ?? null;
        const descriptions = data?.descriptions ?? {};
        const selectedSession = selectedRow?.kind === "session" ? selectedRow.session : null;
        const selectedSessionId = selectedSession?.sessionid;
        const selectedLastActive = selectedSession?.lastactive;
        React.useEffect(() => {
            if (selectedSession == null || !promptsOpen) {
                return;
            }
            // Wait out fast arrow-key scrolling so only the row the user stops on is fetched.
            const timer = setTimeout(() => fireAndForget(() => model.loadPrompts(selectedSession)), 150);
            return () => clearTimeout(timer);
        }, [selectedSessionId, selectedLastActive, promptsOpen]);

        return (
            <div
                ref={model.containerRef}
                tabIndex={0}
                className="absolute inset-0 flex flex-col min-h-0 font-mono text-[13px] leading-none bg-background text-foreground outline-none select-none"
            >
                <div
                    className="flex items-center gap-3 px-2 border-b border-border text-muted-foreground whitespace-nowrap"
                    style={{ height: RowHeight + 4 }}
                >
                    <span className="text-accent">{sessions.length} sessions</span>
                    <span className={running > 0 ? "text-success" : ""}>{running} running</span>
                    {external > 0 ? <span className="text-warning">{external} outside</span> : null}
                    {waiting > 0 ? <span className="text-attention">{waiting} waiting</span> : null}
                    <span>{folderCount} folders</span>
                    {hiddenCount > 0 ? (
                        <span
                            className={cn("cursor-pointer", showHidden ? "text-attention" : "text-muted")}
                            onClick={() => model.toggleShowHidden()}
                            title="removed sessions (H)"
                        >
                            {showHidden ? `showing ${hiddenCount} removed` : `${hiddenCount} removed`}
                        </span>
                    ) : null}
                    {!showOffline ? <span className="text-attention">offline hidden</span> : null}
                    <span className="flex-1" />
                    <span
                        className="text-accent cursor-pointer hover:text-accenthover"
                        onClick={() => model.openAdd()}
                        title="Remember a folder (a)"
                    >
                        + folder
                    </span>
                    <span className="text-muted">? help</span>
                </div>
                {filterOpen ? (
                    <div
                        className="flex items-center gap-2 px-2 border-b border-border"
                        style={{ height: RowHeight + 4 }}
                    >
                        <span className="text-accent">/</span>
                        <input
                            ref={model.filterInputRef}
                            value={filter}
                            onChange={(e) => globalStore.set(model.filterAtom, e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Escape") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    model.closeFilter(true);
                                } else if (e.key === "Enter" || e.key === "ArrowDown") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    model.closeFilter(false);
                                }
                            }}
                            placeholder="filter name, id, folder, description, prompt"
                            className="flex-1 bg-transparent outline-none text-foreground placeholder:text-muted select-text"
                            spellCheck={false}
                        />
                    </div>
                ) : null}
                {edit != null ? (
                    <div
                        className="flex items-center gap-2 px-2 border-b border-border"
                        style={{ height: RowHeight + 4 }}
                    >
                        <span className="text-accent shrink-0 max-w-[40%] truncate">describe {edit.name}</span>
                        <input
                            ref={model.editInputRef}
                            value={editValue}
                            onChange={(e) => globalStore.set(model.editValueAtom, e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Escape") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    model.closeEdit();
                                } else if (e.key === "Enter") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    fireAndForget(() => model.submitEdit());
                                }
                            }}
                            maxLength={500}
                            placeholder="what is this session doing? — Enter saves (empty clears), Esc cancels"
                            className="flex-1 bg-transparent outline-none text-foreground placeholder:text-muted select-text"
                            spellCheck={false}
                        />
                    </div>
                ) : null}
                {addOpen ? (
                    <div
                        className="flex items-center gap-2 px-2 border-b border-border"
                        style={{ height: RowHeight + 4 }}
                    >
                        <span className="text-accent">add folder</span>
                        <input
                            ref={model.addInputRef}
                            value={addValue}
                            onChange={(e) => globalStore.set(model.addValueAtom, e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Escape") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    model.closeAdd();
                                } else if (e.key === "Enter") {
                                    e.preventDefault();
                                    e.stopPropagation();
                                    fireAndForget(() => model.submitAdd());
                                }
                            }}
                            placeholder="absolute path, ~ allowed — Enter adds, Esc cancels"
                            className="flex-1 bg-transparent outline-none text-foreground placeholder:text-muted select-text"
                            spellCheck={false}
                        />
                    </div>
                ) : null}
                {error ? <div className="px-2 py-1 text-error truncate">{error}</div> : null}
                <div ref={listRef} className="flex-1 min-h-[66px] overflow-y-auto overflow-x-hidden">
                    {data == null && !error ? <div className="px-2 py-2 text-muted-foreground">loading…</div> : null}
                    {data != null && rows.length === 0 ? (
                        <div className="px-2 py-2 text-muted-foreground">
                            {filter !== "" ? "no sessions match the filter" : "no Claude Code sessions found"}
                        </div>
                    ) : null}
                    {rows.map((row, i) => {
                        const selected = row.key === effectiveKey;
                        const last = rows[i + 1] == null || rows[i + 1].kind === "group";
                        if (row.kind === "group") {
                            return (
                                <GroupLine
                                    key={row.key}
                                    row={row}
                                    selected={selected}
                                    last={last}
                                    now={now}
                                    home={home}
                                    description=""
                                    model={model}
                                />
                            );
                        }
                        return (
                            <SessionLine
                                key={row.key}
                                row={row}
                                selected={selected}
                                last={last}
                                now={now}
                                home={home}
                                description={descriptions[row.session.sessionid] ?? ""}
                                model={model}
                            />
                        );
                    })}
                </div>
                <div className="flex flex-col shrink-0 max-h-[55%] border-t border-border">
                    <DetailPanel
                        row={selectedRow}
                        home={home}
                        now={now}
                        description={
                            selectedRow?.kind === "session" ? (descriptions[selectedRow.session.sessionid] ?? "") : ""
                        }
                        prompts={selectedRow?.kind === "session" ? prompts[selectedRow.session.sessionid] : null}
                        expanded={promptsOpen}
                        onTogglePrompts={() => model.togglePrompts()}
                    />
                    <div
                        className={cn(
                            "px-2 shrink-0 whitespace-nowrap overflow-hidden truncate",
                            message == null ? "text-muted" : message.isError ? "text-error" : "text-success"
                        )}
                        style={{ height: RowHeight }}
                        title={message?.text}
                    >
                        {message != null
                            ? message.text
                            : "↑↓ move · Enter resume · n new · e describe · a add folder · / filter · ? help"}
                    </div>
                </div>
                {helpOpen ? (
                    <div
                        className="absolute inset-0 flex items-center justify-center bg-background/80"
                        onClick={() => model.toggleHelp()}
                    >
                        <div
                            className="border border-accent bg-modalbg px-4 py-3 max-w-full overflow-auto"
                            onClick={(e) => e.stopPropagation()}
                        >
                            <div className="text-accent mb-2">Claude Sessions — keys</div>
                            {HelpKeys.map(([k, d]) => (
                                <div key={k} className="flex gap-3 leading-5 whitespace-nowrap">
                                    <span className="w-[16ch] shrink-0 text-accent">{k}</span>
                                    <span className="text-foreground">{d}</span>
                                </div>
                            ))}
                            <div className="mt-2 leading-5 text-muted-foreground">
                                <span className="text-success">●</span> busy · <span className="text-accent">○</span>{" "}
                                idle · <span className="text-muted">·</span> offline
                            </div>
                            <div className="mt-2 text-muted">Esc closes</div>
                        </div>
                    </div>
                ) : null}
            </div>
        );
    }
);
ClaudeSessionsView.displayName = "ClaudeSessionsView";
