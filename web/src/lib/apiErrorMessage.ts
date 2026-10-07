import {ApiError} from './api'

const generic:Record<string,string>={
 'invalid request':'Invalid data. Check the fields and try again.',
 'unauthorized':'Your session expired. Sign in again.',
 'forbidden':'You do not have permission to perform this action.',
 'not found':'The item was not found. Refresh the page and try again.',
 'conflict':'The item changed elsewhere. Refresh before trying again.',
 'rate limited':'Too many attempts. Wait a moment and try again.',
 'unavailable':'Service is currently unavailable. Please try again.',
 'internal error':'Could not confirm the result of the operation. Refresh the data before trying again.',
}

export function apiErrorMessage(error:unknown,fallback:string):string{
 if(!(error instanceof ApiError))return fallback
 if(error.status===0)return error.message
 if(error.status===401)return generic.unauthorized
 if(error.status===409&&error.current)return 'This note changed elsewhere. Reconcile the versions before saving.'
 const message=error.message.trim()
 return generic[message.toLowerCase()]||(/^Error \d+$/.test(message)||message==='Unexpected server response.'?fallback:message)
}

export const isUnauthorized=(error:unknown)=>error instanceof ApiError&&error.status===401
