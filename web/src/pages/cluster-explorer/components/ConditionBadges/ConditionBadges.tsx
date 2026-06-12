import type { JSX } from 'react';
import { ExclamationTriangleIcon } from '@heroicons/react/20/solid';
import type { EmbeddedCondition } from '@/api/types.gen';
import { Badge } from '../Badge';
import { strongestConditionTone } from './condition-tone';

/** Compact "N conditions" badge toned by the most severe condition. */
export function ConditionBadges({ conditions }: { conditions: EmbeddedCondition[] }): JSX.Element {
  if (conditions.length === 0) {
    return <></>;
  }

  const strongest = strongestConditionTone(conditions);

  return (
    <Badge tone={strongest}>
      <ExclamationTriangleIcon className="mr-1 size-3" />
      {conditions.length}
    </Badge>
  );
}
