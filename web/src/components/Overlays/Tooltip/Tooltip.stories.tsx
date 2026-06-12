import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { Tooltip } from './Tooltip';

const meta = {
  title: 'Components/Overlays/Tooltip',
  component: Tooltip,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'centered',
    docs: {
      description: {
        component:
          'Hover/focus tooltip on the canonical floating-ui recipe, rendered through a portal so it escapes overflow and stacking contexts. Content must be read-only — never interactive.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof Tooltip>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OnHover: Story = {
  args: {
    content: 'waiting for newer partition insert evidence',
    children: <span className="rounded-md bg-muted/10 px-2 py-0.5 text-xs text-muted">hold</span>,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.hover(canvas.getByText('hold'));
    await waitFor(() => {
      expect(document.body.textContent).toContain('waiting for newer partition insert evidence');
    });
  },
};

export const OnFocus: Story = {
  args: {
    content: 'Pause all tiering writes (dispatch and manual applies)',
    children: (
      <button type="button" className="rounded-md border border-border bg-background px-3 py-1 text-xs text-foreground">
        Pause
      </button>
    ),
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    // Keyboard focus must open the tooltip when the wrapped child is focusable.
    canvas.getByRole('button', { name: 'Pause' }).focus();
    await waitFor(() => {
      expect(document.body.textContent).toContain('Pause all tiering writes');
    });
  },
};

export const DismissesOnUnhover: Story = {
  args: {
    content: 'partition is cold, sealed, and still on hot storage',
    children: <span className="rounded-md bg-success/10 px-2 py-0.5 text-xs text-success">ready</span>,
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.hover(canvas.getByText('ready'));
    await waitFor(() => {
      expect(document.body.textContent).toContain('partition is cold');
    });
    await userEvent.unhover(canvas.getByText('ready'));
    await waitFor(() => {
      expect(document.body.textContent).not.toContain('partition is cold');
    });
  },
};

export const MultiLine: Story = {
  args: {
    content: (
      <div className="space-y-1">
        <div>split partition is waiting for resplit quiet window</div>
        <div className="text-muted">warning: fragmentation_ceiling_exceeded</div>
      </div>
    ),
    children: <span className="rounded-md bg-warning/10 px-2 py-0.5 text-xs text-warning">split</span>,
    placement: 'bottom',
  },
};

export const Placements: Story = {
  args: {
    content: 'tooltip',
    children: <span />,
  },
  render: () => (
    <div className="grid grid-cols-2 gap-8 p-12">
      {(['top', 'bottom', 'left', 'right'] as const).map(placement => (
        <Tooltip key={placement} content={`placement: ${placement}`} placement={placement}>
          <span className="rounded-md border border-border bg-background px-3 py-1 text-xs text-foreground">
            {placement}
          </span>
        </Tooltip>
      ))}
    </div>
  ),
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await userEvent.hover(canvas.getByText('left'));
    await waitFor(() => {
      expect(document.body.textContent).toContain('placement: left');
    });
  },
};

export const InTruncationContext: Story = {
  args: {
    content: 'movoor_dev.test_generic_network_month_local — full table identity in the tooltip',
    children: (
      <span className="truncate font-mono text-xs text-foreground">movoor_dev.test_generic_network_month_local</span>
    ),
    className: 'inline-flex min-w-0 max-w-32',
  },
};
