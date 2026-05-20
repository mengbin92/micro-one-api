export type SortDirection = 'asc' | 'desc' | null;

export interface SortState<T extends object> {
  key: keyof T | null;
  direction: SortDirection;
}

export function getNextSortDirection(direction: SortDirection): SortDirection {
  if (direction === null) return 'asc';
  if (direction === 'asc') return 'desc';
  return null;
}

function compareValues(left: unknown, right: unknown) {
  if (typeof left === 'number' && typeof right === 'number') {
    return left - right;
  }

  const leftNumber = Number(left);
  const rightNumber = Number(right);
  if (Number.isFinite(leftNumber) && Number.isFinite(rightNumber)) {
    return leftNumber - rightNumber;
  }

  return String(left ?? '').localeCompare(String(right ?? ''), undefined, {
    numeric: true,
    sensitivity: 'base',
  });
}

export function sortRows<T extends object>(rows: T[], sort: SortState<T>) {
  if (!sort.key || !sort.direction) {
    return rows;
  }

  return [...rows].sort((left, right) => {
    const result = compareValues(left[sort.key as keyof T], right[sort.key as keyof T]);
    return sort.direction === 'asc' ? result : -result;
  });
}
