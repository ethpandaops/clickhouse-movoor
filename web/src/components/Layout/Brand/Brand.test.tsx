import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Brand } from './Brand';

describe('Brand', () => {
  it('renders the wordmark', () => {
    render(<Brand />);

    expect(screen.getByText('clickhouse-movoor')).toBeInTheDocument();
  });

  it('renders the tagline', () => {
    render(<Brand />);

    expect(screen.getByText('ClickHouse partition tiering')).toBeInTheDocument();
  });

  it('renders the logo as decorative', () => {
    render(<Brand />);

    expect(screen.getByRole('presentation')).toHaveAttribute('src', '/favicon.svg');
  });
});
