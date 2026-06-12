import { useState, type JSX } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ErrorBoundary } from './ErrorBoundary';

function Bomb({ exploded }: { exploded: boolean }): JSX.Element {
  if (exploded) {
    throw new Error('row exploded');
  }
  return <div>healthy content</div>;
}

describe('ErrorBoundary', () => {
  it('renders children when nothing throws', () => {
    render(
      <ErrorBoundary fallback={error => <div>fallback: {error.message}</div>}>
        <Bomb exploded={false} />
      </ErrorBoundary>
    );
    expect(screen.getByText('healthy content')).toBeInTheDocument();
  });

  it('renders the fallback with the thrown error and reports it', () => {
    const onError = vi.fn();
    // React logs caught render errors; keep test output clean.
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);

    render(
      <ErrorBoundary onError={onError} fallback={error => <div>fallback: {error.message}</div>}>
        <Bomb exploded />
      </ErrorBoundary>
    );

    expect(screen.getByText('fallback: row exploded')).toBeInTheDocument();
    expect(onError).toHaveBeenCalledOnce();
    expect(onError.mock.calls[0]?.[0]).toBeInstanceOf(Error);
    consoleError.mockRestore();
  });

  it('recovers via reset once the cause is fixed', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);

    function Harness(): JSX.Element {
      const [exploded, setExploded] = useState(true);
      return (
        <ErrorBoundary
          fallback={(_error, reset) => (
            <button
              type="button"
              onClick={() => {
                setExploded(false);
                reset();
              }}
            >
              Retry
            </button>
          )}
        >
          <Bomb exploded={exploded} />
        </ErrorBoundary>
      );
    }

    render(<Harness />);
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(screen.getByText('healthy content')).toBeInTheDocument();
    consoleError.mockRestore();
  });
});
