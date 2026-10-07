import * as React from 'react'
import {twMerge} from 'tailwind-merge'
export const Input=React.forwardRef<HTMLInputElement,React.InputHTMLAttributes<HTMLInputElement>>(({className,...props},ref)=><input ref={ref} className={twMerge('ui-input',className)} {...props}/>);Input.displayName='Input'
export const Textarea=React.forwardRef<HTMLTextAreaElement,React.TextareaHTMLAttributes<HTMLTextAreaElement>>(({className,...props},ref)=><textarea ref={ref} className={twMerge('ui-input ui-textarea',className)} {...props}/>);Textarea.displayName='Textarea'
