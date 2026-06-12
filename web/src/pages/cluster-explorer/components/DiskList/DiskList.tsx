import type { JSX } from 'react';
import { Badge } from '../Badge';

/** Disk badges; the table's tiering target disk (whatever backs it) is tinted. */
export function DiskList({ disks, targetDisk }: { disks: string[]; targetDisk?: string }): JSX.Element {
  if (disks.length === 0) {
    return <span className="text-xs text-muted">no disk</span>;
  }

  return (
    <div className="flex min-w-0 flex-wrap justify-start gap-1">
      {disks.map(disk => (
        <Badge key={disk} tone={targetDisk !== undefined && disk === targetDisk ? 'info' : 'muted'}>
          {disk}
        </Badge>
      ))}
    </div>
  );
}
