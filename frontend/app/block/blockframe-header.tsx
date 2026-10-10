// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import {
    blockViewToIcon,
    blockViewToName,
    getViewIconElem,
    OptMagnifyButton,
    renderHeaderElements,
} from "@/app/block/blockutil";
import { ConnectionButton } from "@/app/block/connectionbutton";
import { DurableSessionFlyover } from "@/app/block/durable-session-flyover";
import { BadgeRune } from "@/app/element/runes";
import { getBlockBadgeAtom, setBadge } from "@/app/store/badge";
import { getPaneMoveMenuItems } from "@/app/store/blockmove";
import {
    createBlockSplitHorizontally,
    createBlockSplitVertically,
    isDev,
    recordTEvent,
    refocusNode,
    WOS,
} from "@/app/store/global";
import { globalStore } from "@/app/store/jotaiStore";
import { uxCloseBlock } from "@/app/store/keymodel";
import { TabRpcClient } from "@/app/store/wshrpcutil";
import { useWaveEnv } from "@/app/waveenv/waveenv";
import { IconButton } from "@/element/iconbutton";
import { NodeModel } from "@/layout/index";
import { getBadgeVisual } from "@/util/badgekind";
import * as util from "@/util/util";
import { cn, makeIconClass } from "@/util/util";
import * as jotai from "jotai";
import * as React from "react";
import { BlockEnv } from "./blockenv";
import { BlockFrameProps } from "./blocktypes";

const RenamingBlockIdAtom = jotai.atom(null) as jotai.PrimitiveAtom<string>;

function saveBlockTitle(blockEnv: BlockEnv, blockId: string, title: string) {
    const trimmed = title?.trim();
    blockEnv.rpc.SetMetaCommand(TabRpcClient, {
        oref: WOS.makeORef("block", blockId),
        meta: { "frame:title": util.isBlank(trimmed) ? null : trimmed },
    });
}

type BlockTitleEditorProps = {
    blockId: string;
    initialTitle: string;
    className?: string;
};

const BlockTitleEditor = ({ blockId, initialTitle, className }: BlockTitleEditorProps) => {
    const blockEnv = useWaveEnv<BlockEnv>();
    const [value, setValue] = React.useState(initialTitle ?? "");
    const doneRef = React.useRef(false);

    const finish = (save: boolean) => {
        if (doneRef.current) {
            return;
        }
        doneRef.current = true;
        if (save) {
            saveBlockTitle(blockEnv, blockId, value);
        }
        globalStore.set(RenamingBlockIdAtom, null);
        setTimeout(() => refocusNode(blockId), 10);
    };

    return (
        <input
            autoFocus
            className={cn(
                "min-w-[80px] max-w-[300px] rounded border border-accent/60 bg-transparent px-1 text-[12px] font-semibold text-primary outline-none",
                className
            )}
            value={value}
            placeholder="Block name"
            spellCheck={false}
            onFocus={(e) => e.currentTarget.select()}
            onChange={(e) => setValue(e.target.value)}
            onMouseDown={(e) => e.stopPropagation()}
            onDoubleClick={(e) => e.stopPropagation()}
            onKeyDown={(e) => {
                e.stopPropagation();
                if (e.key === "Enter") {
                    e.preventDefault();
                    finish(true);
                } else if (e.key === "Escape") {
                    e.preventDefault();
                    finish(false);
                }
            }}
            onBlur={() => finish(true)}
        />
    );
};

const DebugNotifyDelayMs = 5000;
const DebugNotifyKinds: { label: string; icon: string }[] = [
    { label: "Attention", icon: "bell-exclamation" },
    { label: "Bell", icon: "bell" },
    { label: "Done", icon: "check" },
];

function getDebugNotifyMenuItems(blockId: string): ContextMenuItem[] {
    const send = (icon: string, delayMs: number) => {
        setTimeout(() => setBadge(blockId, { icon, priority: 1000 }), delayMs);
    };
    return [
        {
            label: "Debug: Notify",
            type: "submenu",
            submenu: [
                ...DebugNotifyKinds.map((kind) => ({
                    label: `${kind.label} now`,
                    click: () => send(kind.icon, 0),
                })),
                { type: "separator" as const },
                ...DebugNotifyKinds.map((kind) => ({
                    label: `${kind.label} in ${DebugNotifyDelayMs / 1000}s (switch tab first)`,
                    click: () => send(kind.icon, DebugNotifyDelayMs),
                })),
            ],
        },
    ];
}

function handleHeaderContextMenu(
    e: React.MouseEvent<HTMLDivElement>,
    blockId: string,
    viewModel: ViewModel,
    nodeModel: NodeModel,
    blockEnv: BlockEnv
) {
    e.preventDefault();
    e.stopPropagation();
    const magnified = globalStore.get(nodeModel.isMagnified);
    const currentTitle = globalStore.get(blockEnv.getBlockMetaKeyAtom(blockId, "frame:title"));
    const menu: ContextMenuItem[] = [
        {
            label: magnified ? "Un-Magnify Block" : "Magnify Block",
            click: () => {
                nodeModel.toggleMagnify();
            },
        },
        ...getPaneMoveMenuItems(blockId),
        { type: "separator" },
        {
            label: "Rename Block",
            click: () => {
                globalStore.set(RenamingBlockIdAtom, blockId);
            },
        },
    ];
    if (!util.isBlank(currentTitle)) {
        menu.push({
            label: "Clear Block Name",
            click: () => saveBlockTitle(blockEnv, blockId, null),
        });
    }
    menu.push(
        { type: "separator" },
        {
            label: "Copy BlockId",
            click: () => {
                navigator.clipboard.writeText(blockId);
            },
        }
    );
    const extraItems = viewModel?.getSettingsMenuItems?.();
    if (extraItems && extraItems.length > 0) menu.push({ type: "separator" }, ...extraItems);
    if (isDev()) {
        menu.push({ type: "separator" }, ...getDebugNotifyMenuItems(blockId));
    }
    menu.push(
        { type: "separator" },
        {
            label: "Close Block",
            click: () => uxCloseBlock(blockId),
        }
    );
    blockEnv.showContextMenu(menu, e);
}

type HeaderTextElemsProps = {
    viewModel: ViewModel;
    blockId: string;
    preview: boolean;
    error?: Error;
};

const HeaderTextElems = React.memo(({ viewModel, blockId, preview, error }: HeaderTextElemsProps) => {
    const waveEnv = useWaveEnv<BlockEnv>();
    const frameTextAtom = waveEnv.getBlockMetaKeyAtom(blockId, "frame:text");
    const frameText = jotai.useAtomValue(frameTextAtom);
    let headerTextUnion = util.useAtomValueSafe(viewModel?.viewText);
    headerTextUnion = frameText ?? headerTextUnion;

    const headerTextElems: React.ReactElement[] = [];
    if (typeof headerTextUnion === "string") {
        if (!util.isBlank(headerTextUnion)) {
            headerTextElems.push(
                <div key="text" className="block-frame-text ellipsis">
                    &lrm;{headerTextUnion}
                </div>
            );
        }
    } else if (Array.isArray(headerTextUnion)) {
        headerTextElems.push(...renderHeaderElements(headerTextUnion, preview));
    }
    if (error != null) {
        const copyHeaderErr = () => {
            navigator.clipboard.writeText(error.message + "\n" + error.stack);
        };
        headerTextElems.push(
            <div className="iconbutton disabled" key="controller-status" onClick={copyHeaderErr}>
                <i
                    className="fa-sharp fa-solid fa-triangle-exclamation"
                    title={"Error Rendering View Header: " + error.message}
                />
            </div>
        );
    }

    return <div className="block-frame-textelems-wrapper">{headerTextElems}</div>;
});
HeaderTextElems.displayName = "HeaderTextElems";

type HeaderEndIconsProps = {
    viewModel: ViewModel;
    nodeModel: NodeModel;
    blockId: string;
};

const HeaderEndIcons = React.memo(({ viewModel, nodeModel, blockId }: HeaderEndIconsProps) => {
    const blockEnv = useWaveEnv<BlockEnv>();
    const endIconButtons = util.useAtomValueSafe(viewModel?.endIconButtons);
    const magnified = jotai.useAtomValue(nodeModel.isMagnified);
    const ephemeral = jotai.useAtomValue(nodeModel.isEphemeral);
    const numLeafs = jotai.useAtomValue(nodeModel.numLeafs);
    const magnifyDisabled = numLeafs <= 1;
    const showSplitButtons = jotai.useAtomValue(blockEnv.getSettingsKeyAtom("term:showsplitbuttons"));

    const endIconsElem: React.ReactElement[] = [];

    if (endIconButtons && endIconButtons.length > 0) {
        endIconsElem.push(...endIconButtons.map((button, idx) => <IconButton key={idx} decl={button} />));
    }
    if (showSplitButtons && viewModel?.viewType === "term") {
        const splitHorizontalDecl: IconButtonDecl = {
            elemtype: "iconbutton",
            icon: "columns",
            title: "Split Horizontally",
            click: (e) => {
                e.stopPropagation();
                const blockAtom = WOS.getWaveObjectAtom<Block>(WOS.makeORef("block", blockId));
                const blockData = globalStore.get(blockAtom);
                const blockDef: BlockDef = {
                    meta: blockData?.meta || { view: "term", controller: "shell" },
                };
                createBlockSplitHorizontally(blockDef, blockId, "after");
            },
        };
        const splitVerticalDecl: IconButtonDecl = {
            elemtype: "iconbutton",
            icon: "grip-lines",
            title: "Split Vertically",
            click: (e) => {
                e.stopPropagation();
                const blockAtom = WOS.getWaveObjectAtom<Block>(WOS.makeORef("block", blockId));
                const blockData = globalStore.get(blockAtom);
                const blockDef: BlockDef = {
                    meta: blockData?.meta || { view: "term", controller: "shell" },
                };
                createBlockSplitVertically(blockDef, blockId, "after");
            },
        };
        endIconsElem.push(<IconButton key="split-horizontal" decl={splitHorizontalDecl} />);
        endIconsElem.push(<IconButton key="split-vertical" decl={splitVerticalDecl} />);
    }
    const settingsDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        icon: "cog",
        title: "Settings",
        click: (e) => handleHeaderContextMenu(e, blockId, viewModel, nodeModel, blockEnv),
    };
    endIconsElem.push(<IconButton key="settings" decl={settingsDecl} className="block-frame-settings" />);
    if (ephemeral) {
        const addToLayoutDecl: IconButtonDecl = {
            elemtype: "iconbutton",
            icon: "circle-plus",
            title: "Add to Layout",
            click: () => {
                nodeModel.addEphemeralNodeToLayout();
            },
        };
        endIconsElem.push(<IconButton key="add-to-layout" decl={addToLayoutDecl} />);
    } else {
        endIconsElem.push(
            <OptMagnifyButton
                key="unmagnify"
                magnified={magnified}
                toggleMagnify={() => {
                    nodeModel.toggleMagnify();
                    setTimeout(() => refocusNode(blockId), 50);
                }}
                disabled={magnifyDisabled}
            />
        );
    }

    const closeDecl: IconButtonDecl = {
        elemtype: "iconbutton",
        icon: "xmark-large",
        title: "Close",
        click: () => uxCloseBlock(nodeModel.blockId),
    };
    endIconsElem.push(<IconButton key="close" decl={closeDecl} className="block-frame-default-close" />);

    return <div className="block-frame-end-icons">{endIconsElem}</div>;
});
HeaderEndIcons.displayName = "HeaderEndIcons";

const BlockFrame_Header = ({
    nodeModel,
    viewModel,
    preview,
    connBtnRef,
    changeConnModalAtom,
    error,
}: BlockFrameProps & { changeConnModalAtom: jotai.PrimitiveAtom<boolean>; error?: Error }) => {
    const waveEnv = useWaveEnv<BlockEnv>();
    const metaView = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(nodeModel.blockId, "view"));
    const metaFrameTitle = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(nodeModel.blockId, "frame:title"));
    const metaFrameIcon = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(nodeModel.blockId, "frame:icon"));
    const metaConnection = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(nodeModel.blockId, "connection"));
    let viewName = util.useAtomValueSafe(viewModel?.viewName) ?? blockViewToName(metaView);
    let viewIconUnion = util.useAtomValueSafe(viewModel?.viewIcon) ?? blockViewToIcon(metaView);
    const preIconButton = util.useAtomValueSafe(viewModel?.preIconButton);
    const useTermHeader = util.useAtomValueSafe(viewModel?.useTermHeader);
    const termConfigedDurable = util.useAtomValueSafe(viewModel?.termConfigedDurable);
    const hideViewName = util.useAtomValueSafe(viewModel?.hideViewName);
    const badge = jotai.useAtomValue(getBlockBadgeAtom(useTermHeader ? nodeModel.blockId : null));
    const badgeVisual = getBadgeVisual(badge);
    const magnified = jotai.useAtomValue(nodeModel.isMagnified);
    const prevMagifiedState = React.useRef(magnified);
    const manageConnection = util.useAtomValueSafe(viewModel?.manageConnection);
    const iconColor = jotai.useAtomValue(waveEnv.getBlockMetaKeyAtom(nodeModel.blockId, "icon:color"));
    const dragHandleRef = preview ? null : nodeModel.dragHandleRef;
    const renamingBlockId = jotai.useAtomValue(RenamingBlockIdAtom);
    const isRenaming = !preview && renamingBlockId === nodeModel.blockId;
    const isTerminalBlock = metaView === "term";
    const hasCustomTitle = !util.isBlank(metaFrameTitle);
    viewName = metaFrameTitle ?? viewName;
    viewIconUnion = metaFrameIcon ?? viewIconUnion;

    const startRename = (e: React.MouseEvent) => {
        if (preview) {
            return;
        }
        e.stopPropagation();
        globalStore.set(RenamingBlockIdAtom, nodeModel.blockId);
    };

    React.useEffect(() => {
        if (magnified && !preview && !prevMagifiedState.current) {
            waveEnv.rpc.ActivityCommand(TabRpcClient, { nummagnify: 1 });
            recordTEvent("action:magnify", { "block:view": viewName });
        }
        prevMagifiedState.current = magnified;
    }, [magnified]);

    const viewIconElem = getViewIconElem(viewIconUnion, iconColor);

    return (
        <div
            className={cn("block-frame-default-header", useTermHeader && "!pl-[2px]")}
            data-role="block-header"
            ref={dragHandleRef}
            onContextMenu={(e) => handleHeaderContextMenu(e, nodeModel.blockId, viewModel, nodeModel, waveEnv)}
        >
            {!useTermHeader && (
                <>
                    {preIconButton && <IconButton decl={preIconButton} className="block-frame-preicon-button" />}
                    <div className="block-frame-default-header-iconview">
                        {viewIconElem}
                        {isRenaming ? (
                            <BlockTitleEditor blockId={nodeModel.blockId} initialTitle={metaFrameTitle} />
                        ) : (
                            viewName &&
                            (!hideViewName || hasCustomTitle) && (
                                <div
                                    className="block-frame-view-type"
                                    onDoubleClick={startRename}
                                    title="Double-click to rename"
                                >
                                    {viewName}
                                </div>
                            )
                        )}
                    </div>
                </>
            )}
            {manageConnection && (
                <ConnectionButton
                    ref={connBtnRef}
                    key="connbutton"
                    connection={metaConnection}
                    changeConnModalAtom={changeConnModalAtom}
                    isTerminalBlock={isTerminalBlock}
                />
            )}
            {useTermHeader && termConfigedDurable != null && (
                <DurableSessionFlyover
                    key="durable-status"
                    blockId={nodeModel.blockId}
                    viewModel={viewModel}
                    placement="bottom"
                    divClassName="iconbutton disabled text-[13px] ml-[-4px]"
                />
            )}
            {useTermHeader && badge && (
                <div className="pointer-events-none flex items-center px-1" style={{ color: badge.color || "#ff9e64" }}>
                    {badgeVisual != null ? (
                        <BadgeRune rune={badgeVisual.rune} color={badgeVisual.color} />
                    ) : (
                        <i className={makeIconClass(badge.icon, true, { defaultIcon: "circle-small" })} />
                    )}
                </div>
            )}
            {useTermHeader && isRenaming && (
                <BlockTitleEditor blockId={nodeModel.blockId} initialTitle={metaFrameTitle} className="ml-1" />
            )}
            {useTermHeader && !isRenaming && hasCustomTitle && (
                <div
                    className="ml-1 min-w-0 shrink truncate text-[12px] font-semibold text-primary"
                    onDoubleClick={startRename}
                    title="Double-click to rename"
                >
                    {metaFrameTitle}
                </div>
            )}
            <HeaderTextElems viewModel={viewModel} blockId={nodeModel.blockId} preview={preview} error={error} />
            <HeaderEndIcons viewModel={viewModel} nodeModel={nodeModel} blockId={nodeModel.blockId} />
        </div>
    );
};

export { BlockFrame_Header };
