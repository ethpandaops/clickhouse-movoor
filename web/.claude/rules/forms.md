---
description: Form and validation patterns
globs: src/**/*.tsx, src/**/*.ts
---

# Forms & Validation

## Zod — route search param validation

Zod schemas validate TanStack Router search params, not form submission:

```tsx
import { z } from 'zod';

const searchSchema = z.object({
  page: z.number().optional().default(1),
  filter: z.string().optional(),
});

// In route definition
export const Route = createFileRoute('/items/')({
  validateSearch: searchSchema,
});
```

## react-hook-form — simple form state

Use react-hook-form for filters, inputs, and local form state. No Zod resolvers — keep it simple:

```tsx
import { useForm } from 'react-hook-form';

const { register, watch, handleSubmit } = useForm({
  defaultValues: { search: '', status: 'all' },
});
```

## Placement

- Generic filter components: `src/components/Forms/`
- Page-specific form components: `src/pages/[section]/[page-name]/components/`
- Validation schemas co-located with route or component that uses them
