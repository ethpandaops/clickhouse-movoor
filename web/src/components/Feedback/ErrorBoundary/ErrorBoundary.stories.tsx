import { useState, type JSX } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { ErrorBoundary } from './ErrorBoundary';

function Bomb({ exploded }: { exploded: boolean }): JSX.Element {
  if (exploded) {
    throw new Error('synthetic render crash');
  }
  return <div className="rounded-md border border-border bg-background p-3 text-sm text-foreground">Recovered ✓</div>;
}

const meta = {
  title: 'Components/Feedback/ErrorBoundary',
  component: ErrorBoundary,
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
          'Scoped render-error boundary: one crashing subtree degrades in place with a fallback and a reset hook, instead of blanking the whole page.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof ErrorBoundary>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CatchesAndRecovers: Story = {
  args: {
    children: null,
    fallback: () => null,
  },
  render: function Render() {
    const [exploded, setExploded] = useState(true);
    return (
      <ErrorBoundary
        fallback={(error, reset) => (
          <div className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm">
            <div className="font-medium text-danger">This section crashed</div>
            <div className="mt-0.5 text-xs text-danger/80">{error.message}</div>
            <button
              type="button"
              onClick={() => {
                setExploded(false);
                reset();
              }}
              className="mt-2 rounded-md border border-danger/40 px-2 py-1 text-xs font-medium text-danger transition-colors hover:bg-danger/10"
            >
              Retry
            </button>
          </div>
        )}
      >
        <Bomb exploded={exploded} />
      </ErrorBoundary>
    );
  },
  play: async ({ canvasElement }) => {
    const canvas = within(canvasElement);
    await expect(canvas.getByText('This section crashed')).toBeInTheDocument();
    await expect(canvas.getByText('synthetic render crash')).toBeInTheDocument();
    await userEvent.click(canvas.getByRole('button', { name: 'Retry' }));
    await waitFor(() => {
      expect(canvas.getByText('Recovered ✓')).toBeInTheDocument();
    });
  },
};
