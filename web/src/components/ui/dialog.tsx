import * as React from 'react'
import * as DialogPrimitive from '@radix-ui/react-dialog'
import {X} from 'lucide-react'
import {Tooltip} from '../Tooltip'
export const Dialog=DialogPrimitive.Root
export const DialogTrigger=DialogPrimitive.Trigger
export const DialogTitle=DialogPrimitive.Title
export const DialogDescription=DialogPrimitive.Description
export function DialogContent({children,className,onOpenAutoFocus,onCloseAutoFocus}:React.PropsWithChildren<{className?:string;onOpenAutoFocus?:React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>['onOpenAutoFocus'];onCloseAutoFocus?:React.ComponentPropsWithoutRef<typeof DialogPrimitive.Content>['onCloseAutoFocus']}>){return <DialogPrimitive.Portal><DialogPrimitive.Overlay className="dialog-overlay"/><DialogPrimitive.Content className={'dialog-content '+(className||'')} onOpenAutoFocus={onOpenAutoFocus} onCloseAutoFocus={onCloseAutoFocus}><Tooltip label="Close"><DialogPrimitive.Close className="dialog-close" aria-label="Close"><X size={18}/></DialogPrimitive.Close></Tooltip>{children}</DialogPrimitive.Content></DialogPrimitive.Portal>}
