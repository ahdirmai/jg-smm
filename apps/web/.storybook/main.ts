import type { StorybookConfig } from '@storybook/nextjs';

const config: StorybookConfig = {
  framework: {
    name: '@storybook/nextjs',
    options: {},
  },
  stories: ['../**/*.stories.@(ts|tsx)'],
  addons: ['@storybook/addon-a11y'],
  docs: { autodocs: 'tag' },
  // The app uses path aliases (`@/`) and Tailwind v4 through the workspace
  // tsconfig + postcss, which the Nextjs framework already wires. globals.css
  // is imported in preview so theme tokens resolve in stories too.
  staticDirs: [],
};

export default config;
