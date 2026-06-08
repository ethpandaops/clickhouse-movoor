---
description: Storybook story conventions
globs: src/**/*.stories.tsx
---

# Storybook

## Decorators

Always add the following decorator to new stories:

```tsx
decorators: [
  Story => (
    <div className="min-w-[600px] rounded-xs bg-surface p-6">
      <Story />
    </div>
  ),
],
```

## Story titles

Use the full nested path: `Components/Layout/Container`
