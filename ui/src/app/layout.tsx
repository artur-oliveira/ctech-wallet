import type {Metadata, Viewport} from 'next'
import {Geist, Geist_Mono} from 'next/font/google'
import React from 'react'

import './globals.css'

import {AuthProvider} from '@/lib/context/AuthContext'
import {QueryProvider} from '@/lib/providers/QueryProvider'
import {I18nProvider} from '@/lib/providers/I18nProvider'
import {Toaster} from 'sonner'

const LIGHT_THEME_COLOR = '#f8fafc'

const geistSans = Geist({
    variable: '--font-geist-sans',
    subsets: ['latin'],
})

const geistMono = Geist_Mono({
    variable: '--font-geist-mono',
    subsets: ['latin'],
})

export const viewport: Viewport = {
    width: 'device-width',
    initialScale: 1,
    themeColor: LIGHT_THEME_COLOR,
    viewportFit: 'cover',
    interactiveWidget: 'resizes-content',
}

export const metadata: Metadata = {
    metadataBase: new URL('https://wallet.aoctech.app'),

    title: {
        default: 'CTech Wallet',
        template: '%s | CTech Wallet',
    },

    description:
        'Carteira digital do ecossistema CTech. Centralize seu saldo para pagar assinaturas, utilizar serviços e movimentar dinheiro via PIX.',

    keywords: [
        'CTech Wallet',
        'wallet',
        'digital wallet',
        'pix',
        'saldo',
        'payments',
        'assinaturas',
        'subscription',
        'ecossistema CTech',
    ],

    authors: [
        {
            name: 'CTech',
        },
    ],

    openGraph: {
        title: 'CTech Wallet',
        description:
            'Seu saldo para todo o ecossistema CTech.',
        url: 'https://wallet.aoctech.app',
        siteName: 'CTech Wallet',
        locale: 'pt_BR',
        type: 'website',

        images: [
            {
                url: '/og-image.png',
                width: 1200,
                height: 630,
                alt: 'CTech Wallet',
            },
        ],
    },

    twitter: {
        card: 'summary_large_image',
        title: 'CTech Wallet',
        description:
            'Seu saldo para todo o ecossistema CTech.',
        images: ['/og-image.png'],
    },

    robots: {
        index: false,
        follow: false,
    },

    manifest: '/site.webmanifest',

    icons: {
        icon: '/favicon.ico',
        shortcut: '/favicon.ico',
        apple: '/apple-touch-icon.png',
    },
}

export default function RootLayout({
                                       children,
                                   }: Readonly<{
    children: React.ReactNode
}>) {
    return (
        <html
            lang="pt-BR"
            suppressHydrationWarning
            className={`${geistSans.variable} ${geistMono.variable} h-full`}
        >
        <body
            suppressHydrationWarning
            className="min-h-screen bg-background text-foreground antialiased"
        >
        <QueryProvider>
            <AuthProvider>
                <I18nProvider>{children}</I18nProvider>
            </AuthProvider>
        </QueryProvider>

        <Toaster
            theme="light"
            richColors
            position="top-right"
        />
        </body>
        </html>
    )
}
