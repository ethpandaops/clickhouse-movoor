/**
 * @fileoverview Ban primitive color scales in Tailwind classes
 *
 * This rule prevents developers from using primitive color scales directly
 * (Tailwind palette scales like blue-*, red-*, neutral-*) in Tailwind className strings.
 * All UI colors should use semantic tokens defined in src/index.css.
 *
 * Primitive scales are Tier 1 foundation colors and should only be referenced
 * in the theme definition (src/index.css). Application code should use Tier 2
 * semantic tokens.
 *
 * Examples of incorrect code:
 *   className="bg-neutral-700"
 *   className="text-neutral-500"
 *   className="border-neutral-300"
 *
 * Examples of correct code:
 *   className="bg-primary"
 *   className="text-foreground"
 *   className="border-border"
 *   className="bg-accent"
 */

/** @type {import('eslint').Rule.RuleModule} */
module.exports = {
  meta: {
    type: 'problem',
    docs: {
      description: 'Disallow primitive color scales in Tailwind classes',
      category: 'Best Practices',
      recommended: true,
    },
    messages: {
      primitiveColorScale:
        'Primitive color scale "{{scale}}" detected in "{{match}}". Use semantic tokens instead:\n' +
        '  • Brand: primary, secondary, accent\n' +
        '  • Surface: background, surface, foreground, muted, border\n' +
        '  • State: success, warning, danger',
    },
    schema: [],
  },

  create(context) {
    // Tailwind palette scales that should NOT be used directly in app code
    const primitiveScales = [
      'slate',
      'gray',
      'zinc',
      'neutral',
      'stone',
      'red',
      'orange',
      'amber',
      'yellow',
      'lime',
      'green',
      'emerald',
      'teal',
      'cyan',
      'sky',
      'blue',
      'indigo',
      'violet',
      'purple',
      'fuchsia',
      'pink',
      'rose',
      'brand',
    ];

    // Build regex pattern to match any Tailwind class using primitive scales
    // Matches patterns like: bg-neutral-700, text-neutral-500, border-neutral-300
    const primitivePattern = new RegExp(
      `\\b(?:bg|text|border|from|via|to|ring|outline|decoration|divide|accent|caret|fill|stroke|shadow)-(?:${primitiveScales.join('|')})(?:-(?:50|100|200|300|400|500|600|700|800|900|950))?(?:\\/\\d+)?\\b`,
      'g'
    );

    /**
     * Check if a string value contains primitive color scale usage
     * @param {import('estree').Node} node - The AST node
     * @param {string} value - The string value to check
     */
    function checkForPrimitiveColors(node, value) {
      const matches = value.match(primitivePattern);
      if (matches) {
        matches.forEach(match => {
          // Extract the scale name from the match
          const scaleMatch = primitiveScales.find(scale => match.includes(scale));
          context.report({
            node,
            messageId: 'primitiveColorScale',
            data: {
              scale: scaleMatch,
              match: match,
            },
          });
        });
      }
    }

    return {
      // Handle JSX className attributes
      JSXAttribute(node) {
        if (
          node.name.name === 'className' &&
          node.value &&
          node.value.type === 'Literal' &&
          typeof node.value.value === 'string'
        ) {
          checkForPrimitiveColors(node, node.value.value);
        }

        // Handle template literals in className
        if (
          node.name.name === 'className' &&
          node.value &&
          node.value.type === 'JSXExpressionContainer' &&
          node.value.expression.type === 'TemplateLiteral'
        ) {
          node.value.expression.quasis.forEach(quasi => {
            checkForPrimitiveColors(node, quasi.value.raw);
          });
        }
      },

      // Handle clsx/classnames/cn function calls
      CallExpression(node) {
        const functionName = node.callee.name;
        if (['clsx', 'classnames', 'cn', 'cva'].includes(functionName)) {
          node.arguments.forEach(arg => {
            if (arg.type === 'Literal' && typeof arg.value === 'string') {
              checkForPrimitiveColors(arg, arg.value);
            }
            if (arg.type === 'TemplateLiteral') {
              arg.quasis.forEach(quasi => {
                checkForPrimitiveColors(arg, quasi.value.raw);
              });
            }
          });
        }
      },
    };
  },
};
