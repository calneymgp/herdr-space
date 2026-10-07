import React from 'react'
import {createRoot} from 'react-dom/client'
import {App} from './App'
import './style.css'
// A hard reload can bypass the former Force Agent worker. Refresh only its
// existing root registration; new HERDR visitors do not install a worker.
if ('serviceWorker' in navigator) {
 const scope=new URL('/',location.origin).href
 const script=new URL('/sw.js',location.origin).href
 void navigator.serviceWorker.getRegistration(scope).then(registration=>{
  const worker=registration?.active||registration?.waiting||registration?.installing
  if(registration?.scope===scope&&worker?.scriptURL===script)return registration.update()
 }).catch(()=>{})
}
createRoot(document.getElementById('root')!).render(<React.StrictMode><App/></React.StrictMode>)
