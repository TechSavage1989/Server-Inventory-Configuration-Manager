/* One-second clock for everything that progresses, plus the demo badge. */
(function(){
'use strict';
var X=window.__DEMO_INTERNAL__;
setInterval(function(){
 var t=X.now();
 for(var i=X.timers.length-1;i>=0;i--){if(X.timers[i].at<=t){var f=X.timers[i].fn;X.timers.splice(i,1);try{f();}catch(e){console.error('[demo] timer',e);}}}
 (X.tickers||[]).forEach(function(fn){try{fn();}catch(e){console.error('[demo] ticker',e);}});
},1000);
function badge(){if(document.getElementById('demoBadge')){return;}var b=document.createElement('div');b.id='demoBadge';
 var bar=document.querySelector('.status-bar');b.style.cssText=(bar?'margin-left:auto;margin-right:8px;':'position:fixed;bottom:4px;right:8px;z-index:99998;')+'background:#fff7e6;color:#7a4b00;border:1px solid #f0c36d;border-radius:999px;padding:1px 4px 1px 10px;font:11.5px/1.4 system-ui,Segoe UI,sans-serif;display:flex;gap:8px;align-items:center;flex:none';
 b.innerHTML='<span><b>Demo</b> · fictional data, nothing leaves this page</span><button type="button" style="font:inherit;border:1px solid #e0b25a;background:#fff;border-radius:999px;padding:1px 10px;cursor:pointer;color:inherit">Reset</button>';
 b.querySelector('button').addEventListener('click',function(){location.reload();});if(bar){var k=bar.querySelector('.shortcuts-link');bar.insertBefore(b,k||null);}else{document.body.appendChild(b);}}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',badge);}else{badge();}
})();
