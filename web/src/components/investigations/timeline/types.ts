// TimelineEvent — discriminated union covering every kind of dot or
// bar that can land on the case timeline. Lane key drives both the
// y-axis row and the toggle state in localStorage.

export type TimelineLane =
  | 'lifecycle'
  | 'findings'
  | 'runs'
  | 'notes'
  | 'iocs'
  | 'daimons'
  | 'audit';

export interface TimelineLifecycleEvent {
  kind: 'lifecycle';
  ts: number;
  marker: 'created' | 'closed';
  title: string;
}

export interface TimelineFindingEvent {
  kind: 'finding';
  ts: number;
  id: number;
  severity: 'CRITICAL' | 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO';
  title: string;
  agent: string;
  host: string;
  cpInstanceID?: string;
}

export interface TimelineRunEvent {
  kind: 'run';
  startTs: number;
  endTs: number;
  running: boolean;
  id: number;
  status: 'completed' | 'failed' | 'cancelled' | 'running' | string;
  orchestrationName: string;
}

export interface TimelineNoteEvent {
  kind: 'note';
  ts: number;
  id: number;
  author: string;
  body: string;
}

export interface TimelineIOCEvent {
  kind: 'ioc';
  startTs: number;
  endTs: number;
  id: number;
  iocKind: string;
  value: string;
}

export interface TimelineDaimonEvent {
  kind: 'daimon';
  ts: number;
  agent: string;
  findingID: number;
}

export interface TimelineAuditEvent {
  kind: 'audit';
  ts: number;
  auditKind: 'note' | 'finding_linked' | 'run_linked';
  by: string;
  title: string;
}

export type TimelineEvent =
  | TimelineLifecycleEvent
  | TimelineFindingEvent
  | TimelineRunEvent
  | TimelineNoteEvent
  | TimelineIOCEvent
  | TimelineDaimonEvent
  | TimelineAuditEvent;

export const DEFAULT_LANES_ON: ReadonlyArray<TimelineLane> = ['lifecycle', 'findings', 'runs', 'notes'];
export const ALL_LANES: ReadonlyArray<TimelineLane> = ['lifecycle', 'findings', 'runs', 'notes', 'iocs', 'daimons', 'audit'];
