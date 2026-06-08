import { defineConfig } from '@hey-api/openapi-ts';

const OPENAPI_INPUT = process.env.OPENAPI_INPUT || '../api/openapi.yaml';

export default defineConfig({
  input: OPENAPI_INPUT,
  output: {
    path: 'src/api',
    postProcess: ['prettier'],
  },
  plugins: [
    {
      name: '@hey-api/client-fetch',
      runtimeConfigPath: './src/utils/api-config.ts',
    },
    {
      name: 'zod',
      compatibilityVersion: 'mini',
      metadata: false,
    },
    '@tanstack/react-query',
    {
      name: '@hey-api/sdk',
      validator: 'zod',
    },
  ],
});
