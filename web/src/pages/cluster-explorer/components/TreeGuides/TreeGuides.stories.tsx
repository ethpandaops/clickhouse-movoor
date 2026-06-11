import type { JSX, ReactNode } from 'react';
import type { Meta, StoryObj } from '@storybook/react-vite';
import clsx from 'clsx';
import { TreeGuides, TreeTrunkStart } from './TreeGuides';
import { indentClass } from '../../row-grid';

const meta = {
  title: 'Pages/ClusterExplorer/TreeGuides',
  component: TreeGuides,
  decorators: [
    Story => (
      <div className="min-w-[600px] rounded-xs bg-surface p-6">
        <Story />
      </div>
    ),
  ],
  parameters: {
    docs: {
      description: {
        component:
          'ASCII-style tree guides connecting nested rows to their ancestors. Trunks pass through while an ancestor still has siblings below; the last child turns right and stops.',
      },
    },
  },
  tags: ['autodocs'],
} satisfies Meta<typeof TreeGuides>;

export default meta;
type Story = StoryObj<typeof meta>;

function Row({
  depth,
  trail,
  trunk,
  children,
}: {
  depth: 0 | 1 | 2 | 3;
  trail?: boolean[];
  trunk?: 0 | 1 | 2;
  children: ReactNode;
}): JSX.Element {
  return (
    <div className="relative border-t border-border/50 px-3 py-2 text-sm text-foreground first:border-t-0">
      {trail && <TreeGuides trail={trail} />}
      {trunk !== undefined && <TreeTrunkStart depth={trunk} />}
      <div className={clsx('flex items-center gap-2', indentClass[depth])}>{children}</div>
    </div>
  );
}

export const FullHierarchy: Story = {
  args: { trail: [false] },
  render: () => (
    <div>
      <Row depth={0} trunk={0}>
        <span className="font-semibold">movoor_dev.events_by_month_local</span>
      </Row>
      <Row depth={1} trail={[false]} trunk={1}>
        shard0-replica0
      </Row>
      <Row depth={2} trail={[false, false]} trunk={2}>
        202601
      </Row>
      <Row depth={3} trail={[false, false, true]}>
        <span className="font-mono text-xs">202601_1_1_0</span>
      </Row>
      <Row depth={2} trail={[false, true]}>
        202602
      </Row>
      <Row depth={1} trail={[true]} trunk={1}>
        shard0-replica1
      </Row>
      <Row depth={2} trail={[true, true]}>
        202601
      </Row>
    </div>
  ),
};

export const LastChildElbow: Story = {
  args: { trail: [true] },
  render: () => (
    <div>
      <Row depth={0} trunk={0}>
        <span className="font-semibold">movoor_dev.test_generic_network_month_local</span>
      </Row>
      <Row depth={1} trail={[true]}>
        shard1-replica1 (last child stops the trunk)
      </Row>
    </div>
  ),
};
