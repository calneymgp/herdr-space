import type {Project,Space} from './state'

const cleanPath=(value:string)=>value.replace(/\/+$/,'')

export function spaceSelectionForProject(projectId:string,projects:Project[],spaces:Space[]):string{
 if(!projectId)return ''
 const project=projects.find(item=>item.id===projectId)
 if(!project)return `legacy:${projectId}`
 const space=spaces.find(item=>item.path&&cleanPath(item.path)===cleanPath(project.path))
 return space?`space:${space.id}`:`legacy:${projectId}`
}
