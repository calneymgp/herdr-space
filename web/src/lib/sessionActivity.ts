import type {Session} from './state'

export type SessionActivityStatus={state:'running'|'waiting'|'attention'|'ended';label:string}

export function sessionActivityStatus(session:Session,inventoryStale:boolean):SessionActivityStatus{
 if(inventoryStale)return {state:'attention',label:'Outdated status'}
 if(!session.alive)return {state:'ended',label:'Ended'}
 switch(session.activity){
 case 'working':
 case 'active':return {state:'running',label:'Running'}
 case 'idle':return {state:'waiting',label:'Idle'}
 case 'done':return {state:'waiting',label:'Completed'}
 case 'error':return {state:'attention',label:'Failed'}
 case 'unknown':return {state:'attention',label:'Unknown status'}
 case 'exited':return {state:'attention',label:'Ended'}
 case 'stopped':return {state:'attention',label:'Stopped'}
 default:return {state:'attention',label:'Unknown status'}
 }
}
