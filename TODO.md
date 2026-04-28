# TODO


0. [OPEN] Complete Phase 9 and deploy in OCI
1. [OPEN] Add TokenCount and cost tracking for each Agent, nodes, daimons, global. 
2. [DONE] Add General Dashboard. 
3. [DONE] Better telemetry
4. [OPEN] Add daimons local concept to work as tier-1/2 operators within the CP, to be able to also scale the CP side of things. Some some daimons could also work locally to continue to interact with the data inside the CP because why not. 
5. [WORKING]Too many issues are exactly the same. the threat-intel 
6. Hot-reload default on or off? I argued ON above. Worth your confirmation — some shops want  
  explicit-push semantics for compliance reasons.
7. [DONE] Per-node "freeze" pin — operator can mark a node "do not auto-update". Worth having from day one IMO.                                                                                      
8. [OPEN] Canary cohort selection — random pick? Tagged "canary" nodes? Last-deployed nodes first? I'd default to "1 node from each distinct OS/arch combo" — surfaces compat issues earliest.       
9. [OPEN] Daimon save UX when changes are restart-required — do we just warn at save time and let the operator decide, or queue the save until they explicitly hit "redeploy these daimons"   
10. [OPEN] Telemetry on the rollout itself — when 100 daimons hot-reload, do we want a "rollout progress" view or is the audit log enough?

