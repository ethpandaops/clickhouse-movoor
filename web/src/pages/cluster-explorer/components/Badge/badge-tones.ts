export type BadgeTone = 'danger' | 'info' | 'muted' | 'success' | 'warning';

export const badgeToneClass: Record<BadgeTone, string> = {
  danger: 'bg-danger/10 text-danger ring-1 ring-inset ring-danger/25',
  info: 'bg-primary/10 text-primary ring-1 ring-inset ring-primary/25',
  muted: 'bg-muted/10 text-muted ring-1 ring-inset ring-muted/20',
  success: 'bg-success/10 text-success ring-1 ring-inset ring-success/25',
  warning: 'bg-warning/10 text-warning ring-1 ring-inset ring-warning/25',
};
