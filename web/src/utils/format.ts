const numberFormatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const integerFormatter = new Intl.NumberFormat();
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'short',
  timeStyle: 'short',
});
const timeFormatter = new Intl.DateTimeFormat(undefined, { timeStyle: 'medium' });
const relativeFormatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

/** Parse an API UInt64String defensively; malformed input counts as zero. */
export function toBigInt(value: string | undefined): bigint {
  try {
    return BigInt(value ?? '0');
  } catch {
    return 0n;
  }
}

/** Sum API UInt64Strings without losing precision past 2^53. */
export function sumStrings(values: string[]): string {
  return values.reduce((sum, value) => sum + toBigInt(value), 0n).toString();
}

/** Locale-grouped integer display for API UInt64Strings of any size. */
export function formatInteger(value: string): string {
  if (value === '-') {
    return value;
  }

  const integer = toBigInt(value);
  if (integer <= BigInt(Number.MAX_SAFE_INTEGER)) {
    return integerFormatter.format(Number(integer));
  }

  return integer.toString();
}

/** "3 parts" / "1 row" — formatted count with a naively pluralised noun. */
export function formatCount(value: string, noun: string): string {
  return `${formatInteger(value)} ${noun}${value === '1' ? '' : 's'}`;
}

/** Binary-unit byte display ("18 MiB") from an API UInt64String. */
export function formatBytes(value: string): string {
  const bytes = Number(toBigInt(value));
  if (!Number.isFinite(bytes)) {
    return `${value} B`;
  }

  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  let size = bytes;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }

  return `${numberFormatter.format(size)} ${units[unit]}`;
}

/** Short local date+time; "never" when absent, raw value when unparseable. */
export function formatTimestamp(value: string | undefined): string {
  if (!value) {
    return 'never';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return dateFormatter.format(date);
}

/** Time-of-day only, for dense activity feeds where the date is implied. */
export function formatTimeOnly(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return timeFormatter.format(date);
}

/** "in 3 hours" / "14 minutes ago" for gate countdowns and evidence clocks. */
export function formatRelative(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  const diffSeconds = (date.getTime() - Date.now()) / 1000;
  const abs = Math.abs(diffSeconds);
  if (abs < 60) {
    return relativeFormatter.format(Math.round(diffSeconds), 'second');
  }
  if (abs < 3600) {
    return relativeFormatter.format(Math.round(diffSeconds / 60), 'minute');
  }
  if (abs < 86400) {
    return relativeFormatter.format(Math.round(diffSeconds / 3600), 'hour');
  }
  return relativeFormatter.format(Math.round(diffSeconds / 86400), 'day');
}

/** Human-readable message from an Error, an RFC 9457 problem object, or unknown. */
export function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }

  if (typeof error === 'object' && error !== null && 'detail' in error && typeof error.detail === 'string') {
    return error.detail;
  }

  return 'Request failed';
}
