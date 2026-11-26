// The hook events a scenario's expectations match (§24).

import { type Hooks, type QueryEvent, Redacted, type TxEvent, classOf } from "@dql/runtime";
import type { Json } from "./values.ts";

/**
 * Recorder keeps the hook events an executor fires, each as the fields
 * scenarios match, spelled as Go's harness spells them: a query's end as
 * query, kind, rows, attempt, replica, params (how many parameters it
 * bound), redacted (how many of them are redacted) and class, and a
 * transaction's begin, end, retry and leak as tx with propagation, readonly,
 * savepoint, attempt, isolation, outcome and class.
 */
export class Recorder {
  private readonly events: Json[] = [];

  /** hooks returns the hooks that record into the recorder. */
  hooks(): Hooks {
    const tx =
      (kind: string) =>
      (ev: TxEvent): undefined => {
        this.events.push({
          tx: kind,
          propagation: ev.propagation,
          readonly: ev.readOnly,
          savepoint: ev.savepoint,
          attempt: ev.attempt,
          isolation: ev.isolation ?? "",
          outcome: ev.outcome ?? "",
          class: classOf(ev.error) ?? "",
        });
      };
    return {
      params: true,
      queryEnd: (ev: QueryEvent) => {
        const params = ev.params ?? [];
        this.events.push({
          query: ev.query,
          kind: ev.kind,
          rows: ev.rows,
          attempt: ev.attempt,
          replica: ev.replica,
          params: params.length,
          redacted: params.filter((p) => p === Redacted).length,
          class: ev.class ?? "",
        });
      },
      txBegin: tx("begin"),
      txEnd: tx("end"),
      txRetry: tx("retry"),
      txLeaked: tx("leaked"),
    };
  }

  /** mark returns how many events the recorder holds, and since the events after a mark. */
  mark(): number {
    return this.events.length;
  }

  since(mark: number): Json[] {
    return this.events.slice(mark);
  }
}
