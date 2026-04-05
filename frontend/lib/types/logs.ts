/** A single log entry from Loki. */
export interface LogLine {
  timestamp: string;
  line: string;
  labels: Record<string, string>;
}

/** A volume entry from Loki's volume API. */
export interface VolumeEntry {
  metric: Record<string, string>;
  values: [number, string][];
}
