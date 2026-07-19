'use client'

import * as React from 'react'
import {Dialog as DialogPrimitive} from '@base-ui/react/dialog'
import {X} from 'lucide-react'
import {useTranslation} from 'react-i18next'
import {Button} from '@/components/ui/button'
import {cn} from '@/lib/utils'

function Dialog(props: DialogPrimitive.Root.Props) {
    return <DialogPrimitive.Root data-slot="dialog" {...props}/>
}

function DialogPortal(props: DialogPrimitive.Portal.Props) {
    return <DialogPrimitive.Portal data-slot="dialog-portal" {...props}/>
}

function DialogOverlay({className, ...props}: DialogPrimitive.Backdrop.Props) {
    return (
        <DialogPrimitive.Backdrop
            data-slot="dialog-overlay"
            className={cn(
                'fixed inset-0 z-50 min-h-dvh bg-black/40 duration-150 supports-[-webkit-touch-callout:none]:absolute data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0',
                className,
            )}
            {...props}
        />
    )
}

function DialogContent({
                           className,
                           children,
                           showCloseButton = false,
                           ...props
                       }: DialogPrimitive.Popup.Props & {showCloseButton?: boolean}) {
    const {t} = useTranslation()
    return (
        <DialogPortal>
            <DialogOverlay/>
            <DialogPrimitive.Viewport
                data-slot="dialog-viewport"
                className="fixed inset-0 z-50 flex items-center justify-center overflow-y-auto overscroll-contain [padding-top:max(1rem,env(safe-area-inset-top))] [padding-right:max(1rem,env(safe-area-inset-right))] [padding-bottom:max(1rem,env(safe-area-inset-bottom))] [padding-left:max(1rem,env(safe-area-inset-left))]"
            >
                <DialogPrimitive.Popup
                    data-slot="dialog-content"
                    className={cn(
                        'relative max-h-full min-h-0 w-full overflow-y-auto overscroll-contain rounded-2xl bg-card p-4 text-foreground shadow-modal outline-none duration-150 sm:max-w-sm sm:p-6 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95',
                        className,
                    )}
                    {...props}
                >
                    {children}
                    {showCloseButton && (
                        <DialogPrimitive.Close
                            render={<Button variant="ghost" size="icon-sm" className="absolute right-2 top-2"/>}
                        >
                            <X aria-hidden="true"/>
                            <span className="sr-only">{t('common.close')}</span>
                        </DialogPrimitive.Close>
                    )}
                </DialogPrimitive.Popup>
            </DialogPrimitive.Viewport>
        </DialogPortal>
    )
}

function DialogTitle({className, ...props}: DialogPrimitive.Title.Props) {
    return (
        <DialogPrimitive.Title
            data-slot="dialog-title"
            className={cn('text-lg font-semibold text-foreground', className)}
            {...props}
        />
    )
}

function DialogDescription({className, ...props}: DialogPrimitive.Description.Props) {
    return (
        <DialogPrimitive.Description
            data-slot="dialog-description"
            className={cn('text-sm leading-relaxed text-muted-foreground', className)}
            {...props}
        />
    )
}

export {Dialog, DialogContent, DialogDescription, DialogTitle}
