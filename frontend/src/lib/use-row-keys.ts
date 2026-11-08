import { useState } from "react";

/**
 * Stable React keys for an editable list whose rows have no id of their own
 * (env vars, ports, excluded namespaces), where the parent owns the rows and
 * addresses them by position.
 *
 * A position key would hand row 3's DOM to row 2 when row 2 is removed, and
 * a key built from the row's content changes on every keystroke, which
 * remounts the input and drops focus. Instead each row gets an id when it
 * first appears; new rows at the end get fresh ids, and `removeAt` drops the
 * removed row's id so the rows after it keep theirs. Call it before telling
 * the parent to remove the row.
 */
export interface RowKeys {
  keyAt(index: number): number;
  removeAt(index: number): void;
}

class RowKeyList implements RowKeys {
  private keys: number[] = [];
  private next = 0;

  sync(length: number): void {
    while (this.keys.length < length) this.keys.push(this.next++);
    this.keys.length = length;
  }

  keyAt(index: number): number {
    return this.keys[index];
  }

  removeAt(index: number): void {
    this.keys.splice(index, 1);
  }
}

export function useRowKeys(length: number): RowKeys {
  const [rows] = useState(() => new RowKeyList());
  rows.sync(length);
  return rows;
}
