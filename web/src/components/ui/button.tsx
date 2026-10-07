import * as React from 'react'
import {twMerge} from 'tailwind-merge'
import clsx from 'clsx'
export type ButtonProps=React.ButtonHTMLAttributes<HTMLButtonElement>&{variant?:'default'|'secondary'|'ghost'|'danger'|'outline';size?:'default'|'sm'|'icon'}
export const Button=React.forwardRef<HTMLButtonElement,ButtonProps>(({className,variant='default',size='default',...props},ref)=><button ref={ref} className={twMerge(clsx('ui-button',`ui-button--${variant}`,`ui-button--${size}`,className))} {...props}/>);Button.displayName='Button'
