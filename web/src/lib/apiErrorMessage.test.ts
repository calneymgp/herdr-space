import {describe,expect,it} from 'vitest'
import {ApiError} from './api'
import {apiErrorMessage} from './apiErrorMessage'

describe('api error presentation',()=>{
 it.each([
  ['invalid request',400,'Invalid data'],['unauthorized',401,'Your session expired'],
  ['forbidden',403,'permission'],['not found',404,'was not found'],
  ['conflict',409,'changed elsewhere'],['rate limited',429,'Too many attempts'],
  ['unavailable',503,'unavailable'],['internal error',500,'Could not confirm'],
 ])('translates %s', (token,status,expected)=>{
  expect(apiErrorMessage(new ApiError(token,status),'Failed local')).toContain(expected)
 })
 it('preserves safe public launch text, network message and real conflict current',()=>{
  expect(apiErrorMessage(new ApiError('The agent could not start in this Space.',503),'Failed')).toBe('The agent could not start in this Space.')
  expect(apiErrorMessage(new ApiError('No connection to the server. Your local changes were preserved.',0),'Failed')).toContain('No connection')
  expect(apiErrorMessage(new ApiError('conflict',409,{id:'A'} as never),'Failed')).toContain('Reconcile')
  expect(apiErrorMessage(new Error('stack detail'),'Failed local')).toBe('Failed local')
 })
})
