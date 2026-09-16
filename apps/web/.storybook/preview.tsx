import type { Preview } from '@storybook/nextjs';

import '../app/globals.css';
import { ThemeProvider } from '../components/theme-provider';

const preview: Preview = {
  parameters: {
    a11y: {
      // axe runs on every story; the CI test-runner fails the build on a
      // violation rather than only flagging it in the addons panel.
      element: '#storybook-root',
      manual: false,
    },
    controls: { matchers: { color: /(background|color)$/i, date: /Date$/i } },
  },
  // Stories cover both themes: the canvas toggles `class` on the root, which is
  // exactly how the real dashboard switches (next-themes attribute="class").
  decorators: [
    (Story, context) => (
      <ThemeProvider>
        <div data-theme={context.globals.theme} className="p-4">
          <Story />
        </div>
      </ThemeProvider>
    ),
  ],
  globalTypes: {
    theme: {
      name: 'Theme',
      description: 'Light or dark, matching the dashboard toggle.',
      defaultValue: 'dark',
      toolbar: {
        icon: 'circlehollow',
        items: [
          { value: 'light', icon: 'sun', title: 'Light' },
          { value: 'dark', icon: 'moon', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
  },
};

export default preview;
