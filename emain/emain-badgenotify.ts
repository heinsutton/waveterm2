// Copyright 2026, Command Line Inc.
// SPDX-License-Identifier: Apache-2.0

import { ObjectService } from "@/app/store/services";
import { waveEventSubscribeSingle } from "@/app/store/wps";
import { RpcApi } from "@/app/store/wshclientapi";
import { BadgeKind, cmpBadge, getBadgeKind, shouldNotifyForBadge } from "@/util/badgekind";
import { fireAndForget } from "@/util/util";
import { Notification } from "electron";
import { getAllWaveWindows, revealBlock } from "./emain-window";
import { ElectronWshClient } from "./emain-wsh";

const KindPhrases: Record<BadgeKind, string> = {
    attention: "needs attention",
    bell: "rang the bell",
    done: "is done",
};

const badgeMirror = new Map<string, Badge>();
const liveNotifications = new Set<Notification>();
let seeding = false;
let seedClearedAll = false;
const seedTouched = new Set<string>();

function isLookingAtTab(tabId: string): boolean {
    return getAllWaveWindows().some(
        (ww) => !ww.isDestroyed() && ww.isFocused() && ww.activeTabView?.waveTabId === tabId
    );
}

async function resolveBadgeContext(oref: string): Promise<{ tabId: string; blockId: string; paneName: string } | null> {
    let curOref = oref;
    let blockId: string = null;
    let paneName: string = null;
    for (let depth = 0; depth < 8; depth++) {
        const sep = curOref.indexOf(":");
        const otype = curOref.slice(0, sep);
        const oid = curOref.slice(sep + 1);
        if (otype === "tab") {
            return { tabId: oid, blockId, paneName };
        }
        if (otype !== "block") {
            return null;
        }
        const block = (await ObjectService.GetObject(curOref)) as Block;
        if (block == null) {
            return null;
        }
        const title = block.meta?.["frame:title"]?.trim();
        if (paneName == null && title) {
            paneName = title;
        }
        blockId = oid;
        if (!block.parentoref) {
            return null;
        }
        curOref = block.parentoref;
    }
    return null;
}

async function notifyForBadge(oref: string, badge: Badge) {
    const kind = getBadgeKind(badge);
    if (kind == null || badge.pidlinked) {
        return;
    }
    const fullConfig = await RpcApi.GetFullConfigCommand(ElectronWshClient);
    const enabledKinds = fullConfig?.settings?.["app:notifybadges"];
    if (!shouldNotifyForBadge({ kind, enabledKinds, isLooking: false })) {
        return;
    }
    const context = await resolveBadgeContext(oref);
    if (context == null) {
        return;
    }
    if (isLookingAtTab(context.tabId)) {
        return;
    }
    const tab = (await ObjectService.GetObject(`tab:${context.tabId}`)) as Tab;
    if (tab == null) {
        return;
    }
    const tabName = tab.name || "Tab";
    const phrase = KindPhrases[kind];
    const paneLabel = context.paneName != null ? `${context.paneName} - ${tabName}` : null;
    const notification = new Notification({
        title: paneLabel ?? tabName,
        body: paneLabel != null ? `${paneLabel}: ${phrase}` : `${tabName} ${phrase}`,
    });
    liveNotifications.add(notification);
    const release = () => liveNotifications.delete(notification);
    notification.on("click", () => {
        release();
        fireAndForget(() => revealBlock(context.tabId, context.blockId));
    });
    notification.on("close", release);
    notification.on("failed", release);
    notification.show();
}

function handleBadgeEvent(data: BadgeEvent) {
    if (data == null) {
        return;
    }
    if (data.clearall) {
        badgeMirror.clear();
        if (seeding) {
            seedClearedAll = true;
        }
        return;
    }
    if (data.oref == null) {
        return;
    }
    if (seeding) {
        seedTouched.add(data.oref);
    }
    if (data.clearbyid) {
        if (badgeMirror.get(data.oref)?.badgeid === data.clearbyid) {
            badgeMirror.delete(data.oref);
        }
        return;
    }
    if (data.clear) {
        badgeMirror.delete(data.oref);
        return;
    }
    if (data.badge == null) {
        return;
    }
    const existing = badgeMirror.get(data.oref);
    if (existing != null && cmpBadge(data.badge, existing) <= 0) {
        return;
    }
    badgeMirror.set(data.oref, data.badge);
    fireAndForget(() => notifyForBadge(data.oref, data.badge));
}

export function initBadgeNotifications() {
    waveEventSubscribeSingle({
        eventType: "badge",
        handler: (event) => handleBadgeEvent(event.data as BadgeEvent),
    });
    fireAndForget(async () => {
        seeding = true;
        try {
            const badges = await RpcApi.GetAllBadgesCommand(ElectronWshClient);
            for (const badgeEvent of badges ?? []) {
                if (seedClearedAll || badgeEvent.oref == null || badgeEvent.badge == null) {
                    continue;
                }
                if (!seedTouched.has(badgeEvent.oref) && !badgeMirror.has(badgeEvent.oref)) {
                    badgeMirror.set(badgeEvent.oref, badgeEvent.badge);
                }
            }
        } finally {
            seeding = false;
            seedClearedAll = false;
            seedTouched.clear();
        }
    });
}
