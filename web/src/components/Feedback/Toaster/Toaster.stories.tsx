import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect, userEvent, waitFor, within } from 'storybook/test';
import { Toaster } from './Toaster';
import { clearToasts, pushToast } from './toast-store';

const meta = {
  title: 'Components/Feedback/Toaster',
  component: Toaster,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <div className="text-sm text-muted">Page content — toasts stack bottom-right.</div>
        <Story />
      </div>
    ),
  ],
  parameters: {
    layout: 'fullscreen',
    docs: {
      description: {
        component:
          'Backstop notification channel for failures with no inline home (unhandled mutation errors). Error toasts persist until dismissed.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof Toaster>;

export default meta;
type Story = StoryObj<typeof meta>;

export const PushAndDismiss: Story = {
  play: async () => {
    clearToasts();
    pushToast({ title: 'Action failed', detail: 'pause tiering: controller is shutting down' });
    pushToast({ title: 'Action failed', detail: 'state token is stale: partition advanced since plan render' });

    const body = within(document.body);
    await waitFor(() => {
      expect(body.getAllByRole('alert')).toHaveLength(2);
    });
    await expect(body.getByText(/controller is shutting down/)).toBeInTheDocument();

    await userEvent.click(body.getAllByRole('button', { name: 'Dismiss notification' })[0]!);
    await waitFor(() => {
      expect(body.getAllByRole('alert')).toHaveLength(1);
    });

    clearToasts();
    await waitFor(() => {
      expect(body.queryByRole('alert')).toBeNull();
    });
  },
};
