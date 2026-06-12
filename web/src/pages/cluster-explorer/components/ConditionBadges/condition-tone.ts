import type { EmbeddedCondition } from '@/api/types.gen';
import type { BadgeTone } from '../Badge';

/** Tone of the most severe condition in the list. */
export function strongestConditionTone(conditions: EmbeddedCondition[]): BadgeTone {
  if (conditions.some(condition => condition.severity === 'critical')) {
    return 'danger';
  }
  if (conditions.some(condition => condition.severity === 'warning')) {
    return 'warning';
  }

  return 'info';
}
