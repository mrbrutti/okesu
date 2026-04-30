import { defineConfig } from 'astro/config';
import tailwind from '@astrojs/tailwind';
import mdx from '@astrojs/mdx';
import sitemap from '@astrojs/sitemap';

// Astro config for okesu.to.
//
// site:    canonical URL — feeds <link rel="canonical">, sitemap, OG tags.
// output:  static — pure HTML/JS, GitHub Pages serves dist/ directly.
// trailingSlash: never — GitHub Pages serves /concepts/daimons.html cleanly
//          without a trailing slash; matches what the CNAME-bound domain
//          resolves to in practice.
export default defineConfig({
  site: 'https://okesu.to',
  output: 'static',
  trailingSlash: 'never',
  integrations: [
    tailwind({ applyBaseStyles: true }),
    mdx(),
    sitemap(),
  ],
});
