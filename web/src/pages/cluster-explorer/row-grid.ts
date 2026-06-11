/**
 * Responsive row template shared by every level:
 * - base (<md): identity + bytes; the rest collapses into a stacked meta line
 * - md-xl: five columns (name, engine/disk, rows, bytes, tiering)
 * - xl+: all eight columns
 * Cells 2-7 carry the matching visibility classes below, in DOM order.
 */
export const rowGridClass =
  'relative grid items-center gap-3 grid-cols-[minmax(0,1fr)_minmax(4.5rem,auto)] md:grid-cols-[minmax(0,1.7fr)_minmax(0,0.75fr)_minmax(4.5rem,0.4fr)_minmax(5.5rem,0.45fr)_minmax(7.5rem,0.5fr)] xl:grid-cols-[minmax(20rem,1.55fr)_minmax(10rem,0.75fr)_minmax(8rem,0.6fr)_minmax(6rem,0.45fr)_minmax(5rem,0.4fr)_minmax(6rem,0.45fr)_minmax(6.5rem,0.5fr)_minmax(7.5rem,0.55fr)]';

export const colEngineClass = 'max-md:hidden';
export const colShardClass = 'max-xl:hidden';
export const colPartitionsClass = 'max-xl:hidden';
export const colPartsClass = 'max-xl:hidden';
export const colRowsClass = 'max-md:hidden';
export const colTieringClass = 'max-md:hidden';

/** Stacked stats line shown under a nested row's identity on mobile only. */
export const mobileMetaClass = 'mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted md:hidden';

/** Indentation per tree level: tighter on mobile, roomier from md up. */
export const indentClass = ['', 'pl-4 md:pl-5', 'pl-8 md:pl-10', 'pl-12 md:pl-16'] as const;
