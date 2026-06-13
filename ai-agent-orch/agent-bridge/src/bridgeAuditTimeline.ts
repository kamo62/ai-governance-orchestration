import { TimelineEntry, nextMessageId } from './bridgeAgentTypes';
import { AuditEventRecord } from './bridgeGovernanceClient';

export function timelineFromAuditEvents(events: AuditEventRecord[]): TimelineEntry[] {
    return events.map((event) => ({
        id: event.event_id || nextMessageId(),
        label: formatEventType(event.event_type || 'event'),
        detail: [event.agent, event.reason].filter(Boolean).join(' — ') || undefined,
        timestamp: parseAuditTime(event.recorded_at),
    }));
}

function formatEventType(value: string): string {
    return value.replace(/\./g, ' ').replace(/_/g, ' ');
}

function parseAuditTime(value?: string): number {
    if (!value) {
        return Date.now();
    }
    const parsed = Date.parse(value);
    return Number.isNaN(parsed) ? Date.now() : parsed;
}