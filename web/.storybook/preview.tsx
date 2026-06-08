import type { Preview, ReactRenderer } from '@storybook/react-vite';
import { INITIAL_VIEWPORTS } from 'storybook/viewport';
import { RouterProvider, createMemoryHistory, createRootRoute, createRouter } from '@tanstack/react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ThemeProvider } from '../src/providers/ThemeProvider';
import { initialize, mswLoader } from 'msw-storybook-addon';
import '../src/index.css';

type StorybookThemeMode = 'light' | 'dark' | 'system';

// Initialize MSW
initialize({
  onUnhandledRequest: 'bypass',
  quiet: true,
});

const preview: Preview = {
  globalTypes: {
    themeMode: {
      name: 'Theme',
      description: 'Global theme mode',
      defaultValue: 'system',
      toolbar: {
        icon: 'mirror',
        dynamicTitle: true,
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
          { value: 'system', title: 'System' },
        ],
      },
    },
  },
  loaders: [mswLoader],
  decorators: [
    (Story, context) => {
      const themeMode = (context.globals.themeMode as StorybookThemeMode | undefined) ?? 'system';

      if (typeof window !== 'undefined') {
        localStorage.setItem('theme', themeMode);
      }

      const queryOptions = context.parameters.tanstackQuery?.queries || {};

      const queryClient = new QueryClient({
        defaultOptions: {
          queries: {
            retry: 0,
            staleTime: Infinity,
            ...queryOptions,
          },
        },
      });

      const routerConfig = context.parameters.router || {};
      const initialUrl = routerConfig.initialUrl || '/';
      const initialSearch = routerConfig.initialSearch || {};

      const rootRoute = createRootRoute({
        component: () => <Story />,
        validateSearch: () => initialSearch,
      });

      const searchString =
        Object.keys(initialSearch).length > 0
          ? '?' + new URLSearchParams(initialSearch as Record<string, string>).toString()
          : '';

      const router = createRouter({
        routeTree: rootRoute,
        history: createMemoryHistory({
          initialEntries: [initialUrl + searchString],
        }),
        defaultPendingMinMs: 0,
      });

      return (
        <QueryClientProvider client={queryClient} key={context.id}>
          <ThemeProvider key={`theme-${themeMode}`}>
            <RouterProvider router={router} />
          </ThemeProvider>
        </QueryClientProvider>
      );
    },
  ] as ReactRenderer['decorators'],
  parameters: {
    msw: {
      handlers: [],
    },
    viewport: {
      viewports: INITIAL_VIEWPORTS,
    },
    controls: {
      matchers: {
        color: /(background|color)$/i,
        date: /Date$/i,
      },
    },
    options: {
      storySort: {
        method: 'alphabetical',
      },
    },
  },
};

export default preview;
