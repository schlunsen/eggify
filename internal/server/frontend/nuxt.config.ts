const devBackend = process.env.EGGIFY_DEV_BACKEND || 'https://localhost:3333'

// https://nuxt.com/docs/api/configuration/nuxt-config
export default defineNuxtConfig({
  compatibilityDate: '2024-11-01',
  // Devtools only in development; production builds should not carry them.
  devtools: { enabled: process.env.NODE_ENV !== 'production' },

  // PWA Module Configuration
  modules: [
    '@pinia/nuxt',
    // '@vite-pwa/nuxt', // TEMPORARILY DISABLED for OAuth debugging
    '@nuxt/icon'
  ],

  // Pinia Configuration
  pinia: {
    storeDir: './app/stores'
  },

  // TEMPORARILY DISABLED for OAuth debugging - Service worker was causing issues with OAuth callback
  // pwa: {
  //   registerType: 'autoUpdate',
  //   workbox: {
  //     globPatterns: ['**/*.{js,css,html,ico,png,svg}'],
  //     navigateFallback: '/',
  //     // Skip waiting for service worker updates to ensure immediate activation
  //     skipWaiting: true,
  //     clientsClaim: true,
  //     // Handle self-signed certificates gracefully
  //     mode: process.env.NODE_ENV === 'development' ? 'development' : 'production',
  //     runtimeCaching: [
  //       {
  //         urlPattern: /^https:\/\/fonts\.googleapis\.com\/.*/i,
  //         handler: 'CacheFirst',
  //         options: {
  //           cacheName: 'google-fonts-cache',
  //           expiration: {
  //             maxEntries: 10,
  //             maxAgeSeconds: 60 * 60 * 24 * 365 // 365 days
  //           },
  //           cacheableResponse: {
  //             statuses: [0, 200]
  //           }
  //         }
  //       },
  //       {
  //         urlPattern: /^https:\/\/fonts\.gstatic\.com\/.*/i,
  //         handler: 'CacheFirst',
  //         options: {
  //           cacheName: 'gstatic-fonts-cache',
  //           expiration: {
  //             maxEntries: 10,
  //             maxAgeSeconds: 60 * 60 * 24 * 365 // 365 days
  //           },
  //           cacheableResponse: {
  //             statuses: [0, 200]
  //           }
  //         }
  //       },
  //       {
  //         urlPattern: /^https:\/\/localhost:3333\/api\/.*/i,
  //         handler: 'NetworkFirst',
  //         options: {
  //           cacheName: 'api-cache',
  //           networkTimeoutSeconds: 10,
  //           expiration: {
  //             maxEntries: 100,
  //             maxAgeSeconds: 60 * 5 // 5 minutes
  //           },
  //           cacheableResponse: {
  //             statuses: [0, 200]
  //           }
  //         }
  //       }
  //     ]
  //   },
  //   manifest: {
  //     name: 'Eggify',
  //     short_name: 'Eggify',
  //     description: 'Real-time analytics dashboard for Claude Code',
  //     theme_color: '#667eea',
  //     background_color: '#1a1a1a',
  //     display: 'standalone',
  //     orientation: 'any',
  //     scope: '/',
  //     start_url: '/',
  //     categories: ['productivity', 'utilities'],
  //     lang: 'en',
  //     dir: 'ltr',
  //     shortcuts: [
  //       {
  //         name: 'Analytics Dashboard',
  //         short_name: 'Dashboard',
  //         description: 'View real-time analytics',
  //         url: '/',
  //         icons: [
  //           {
  //             src: '/pwa-192x192.png',
  //             sizes: '192x192',
  //             type: 'image/png'
  //           }
  //         ]
  //       },
  //       {
  //         name: 'Live Agents',
  //         short_name: 'Agents',
  //         description: 'Manage Claude agents',
  //         url: '/agents',
  //         icons: [
  //           {
  //             src: '/pwa-192x192.png',
  //             sizes: '192x192',
  //             type: 'image/png'
  //           }
  //         ]
  //       }
  //     ],
  //     icons: [
  //       {
  //         src: '/pwa-192x192.png',
  //         sizes: '192x192',
  //         type: 'image/png',
  //         purpose: 'any'
  //       },
  //       {
  //         src: '/pwa-512x512.png',
  //         sizes: '512x512',
  //         type: 'image/png',
  //         purpose: 'any'
  //       },
  //       {
  //         src: '/pwa-512x512.png',
  //         sizes: '512x512',
  //         type: 'image/png',
  //         purpose: 'maskable'
  //       }
  //     ]
  //   },
  //   devOptions: {
  //     enabled: true,
  //     type: 'module'
  //   },
  //   client: {
  //     installPrompt: true,
  //     periodicSyncForUpdates: 3600
  //   }
  // },

  // SPA mode for real-time dashboard
  ssr: false,

  // Generate static SPA for Go server
  nitro: {
    preset: 'static',
    prerender: {
      crawlLinks: false,
      routes: ['/']
    }
  },

  // Route rules for SPA mode
  routeRules: {
    '/': { prerender: true }
  },

  // Development server configuration
  devServer: {
    port: 3001
  },

  // Vite configuration for API proxy. EGGIFY_DEV_BACKEND points the dev
  // server at another backend (default: the CLI on https://localhost:3333).
  vite: {
    server: {
      proxy: {
        '/api': {
          target: devBackend,
          changeOrigin: true,
          secure: false // Accept self-signed certificates
        },
        '/ws': {
          target: devBackend.replace(/^http/, 'ws'),
          ws: true,
          secure: false // Accept self-signed certificates
        },
        '/agent/ws': {
          target: devBackend.replace(/^http/, 'ws'),
          ws: true,
          secure: false // Accept self-signed certificates
        }
      }
    },
    optimizeDeps: {
      include: ['@huggingface/transformers', 'onnxruntime-web'],
      exclude: ['parakeet.js']
    },
    worker: {
      format: 'es' as const,
      rollupOptions: {
        output: {
          // Name worker assets like the main client build (`_nuxt/[name].[hash].[ext]`)
          // instead of Vite's worker default (`[name]-[hash]`). The parakeet worker
          // and the main bundle both reference onnxruntime's ~21 MB wasm; with
          // matching names they resolve to one file instead of two copies.
          assetFileNames: '_nuxt/[name].[hash][extname]',
        },
      },
    },
    build: {
      rollupOptions: {
        external: []
      }
    }
  },

  // Page transition configuration
  app: {
    pageTransition: {
      name: 'page',
      mode: 'out-in'
    },
    head: {
      title: 'Eggify - AI Control Center',
      meta: [
        { charset: 'utf-8' },
        { name: 'viewport', content: 'width=device-width, initial-scale=1' },
        { name: 'description', content: 'Real-time analytics dashboard for Claude Code' }
      ],
      link: [
        // Animated SVG favicon (matches login page logo)
        { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' },
        { rel: 'alternate icon', href: '/favicon.ico' },
        // Google Fonts for theme-specific typography
        { rel: 'preconnect', href: 'https://fonts.googleapis.com' },
        { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: '' },
        // Default: Inter (modern, clean)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap' },
        // Neon: Orbitron (futuristic, cyberpunk)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Orbitron:wght@400;500;600;700;800;900&display=swap' },
        // Nord: Fira Code (developer-focused with ligatures)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Fira+Code:wght@300;400;500;600;700&display=swap' },
        // Nord: Fira Sans (companion to Fira Code)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Fira+Sans:wght@400;500;600;700&display=swap' },
        // Dracula: JetBrains Mono (professional coding font)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600;700&display=swap' },
        // Eggify: Questrial (clean, modern sans-serif)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Questrial&display=swap' },
        // Flying Circus: circus-poster headings, engraved-book body, typewriter code
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Rye&family=EB+Garamond:ital,wght@0,400;0,500;0,600;0,700;1,400&family=Courier+Prime:wght@400;700&display=swap' },
        // Puppet Theatre (default): playbill headings (Courier Prime comes from the line above)
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Fraunces:opsz,wght@9..144,400..700&display=swap' },
        // Motleyard: geometric workshop typography and compact code
        { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Space+Grotesk:wght@400;500;600;700&family=DM+Mono:wght@400;500&display=swap' }
      ],
      script: [
        {
          innerHTML: `
            // Console swag for Eggify - Epic Edition
            (function() {
              console.log(' '); // spacing

              // Main title with gradient
              console.log('%c🎮  EGGIFY  🚀',
                'font-size: 24px; font-weight: bold; padding: 20px 0; text-align: center; ' +
                'background: linear-gradient(135deg, #667eea 0%, #764ba2 50%, #f093fb 100%); ' +
                '-webkit-background-clip: text; -webkit-text-fill-color: transparent; ' +
                'letter-spacing: 0.1em; display: block; text-align: center;'
              );

              console.log(' '); // spacing

              // Links section
              console.log('%c🌐 LINKS',
                'color: #FFC107; font-weight: bold; font-size: 16px; padding: 8px 0;'
              );

              console.log(
                '%c🛠️  GitHub:%c https://github.com/schlunsen/eggify',
                'color: #0088aa; font-weight: bold; font-size: 13px;',
                'color: #00ffff; text-decoration: underline; font-size: 13px;'
              );

              console.log(
                '%c📖 Docs:%c https://schlunsen.github.io/eggify/',
                'color: #0088aa; font-weight: bold; font-size: 13px; padding-left: 6px;',
                'color: #00ffff; text-decoration: underline; font-size: 13px;'
              );

              console.log(' '); // spacing

              // Developer credit
              console.log(
                '%cDeveloped by%c https://github.com/schlunsen/',
                'color: #888; font-style: italic; font-size: 12px;',
                'color: #aaa; font-style: italic; font-size: 12px; text-decoration: underline;'
              );

              console.log(' '); // final spacing
            })();
          `,
          type: 'text/javascript'
        }
      ]
    }
  }
})
