import {useCallback,useLayoutEffect,useRef,useState,type HTMLAttributes,type ReactNode,type UIEvent} from 'react'
type Direction='left'|'right'|'up'|'down'
type ScrollCueProps=HTMLAttributes<HTMLDivElement>&{className:string;children:ReactNode}

export function ScrollCue({children,className,onScroll,...attributes}:ScrollCueProps){
 const scroller=useRef<HTMLDivElement>(null)
 const [direction,setDirection]=useState<Direction|null>(null)
 const measure=useCallback(()=>{
  const element=scroller.current
  if(!element)return
  const overflowsX=element.scrollWidth>element.clientWidth
  const overflowsY=element.scrollHeight>element.clientHeight
  let next:Direction|null=null
  if(overflowsX){
   const hasContentLeft=element.scrollLeft>0
   const hasContentRight=element.scrollLeft+element.clientWidth<element.scrollWidth
   next=hasContentRight?'right':hasContentLeft?'left':null
  }else if(overflowsY){
   const hasContentAbove=element.scrollTop>0
   const hasContentBelow=element.scrollTop+element.clientHeight<element.scrollHeight
   next=hasContentBelow?'down':hasContentAbove?'up':null
  }
  setDirection(current=>current===next?current:next)
 },[])
 useLayoutEffect(()=>{
  const element=scroller.current
  if(!element)return
  measure()
  const resizeObserver=typeof ResizeObserver==='undefined'?undefined:new ResizeObserver(measure)
  const observeChildren=()=>{
   resizeObserver?.disconnect()
   resizeObserver?.observe(element)
   for(const child of element.children)resizeObserver?.observe(child)
  }
  observeChildren()
  const mutationObserver=typeof MutationObserver==='undefined'?undefined:new MutationObserver(records=>{
   if(records.some(record=>record.type==='childList'))observeChildren()
   measure()
  })
  mutationObserver?.observe(element,{childList:true,subtree:true,characterData:true})
  window.addEventListener('resize',measure)
  return ()=>{
   resizeObserver?.disconnect()
   mutationObserver?.disconnect()
   window.removeEventListener('resize',measure)
  }
 },[measure])
 const handleScroll=(event:UIEvent<HTMLDivElement>)=>{onScroll?.(event);measure()}
 return <div className="scroll-cue" data-scroll-cue={direction||undefined}><div {...attributes} ref={scroller} className={className} onScroll={handleScroll}>{children}</div><span className="scroll-cue-hint" aria-hidden="true"/></div>
}
