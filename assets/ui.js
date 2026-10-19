(function(){
"use strict";
var NS="http://www.w3.org/2000/svg";
var dlg=document.getElementById("om-dlg"),badge=document.getElementById("om-badge"),alertsBox=document.getElementById("om-alerts");
var live=badge&&badge.getAttribute("data-live")==="1",last=null;
function $(s,r){return (r||document).querySelector(s)}
function main(){return document.getElementById("om-main")}

/* ---------- pop-up port editor ---------- */
function body(){return $(".om-body",dlg)}
function openPort(href,alertId){
  if(!dlg||!dlg.showModal)return false;
  var u=href+(href.indexOf("?")<0?"?":"&")+"fragment=1"+(alertId?"&alert="+encodeURIComponent(alertId):"");
  return fetch(u,{credentials:"same-origin"}).then(function(r){return r.text().then(function(t){return {ok:r.ok,t:t}})}).then(function(x){
    if(!x.ok){location.href=href;return}
    body().innerHTML=x.t;
    if(!dlg.open)dlg.showModal();
  },function(){location.href=href});
}
function closeDlg(){if(dlg&&dlg.open)dlg.close()}
function refreshMap(){
  return fetch("/?fragment=1",{credentials:"same-origin"}).then(function(r){return r.text()}).then(function(t){
    main().innerHTML=t;
    return fetch("/live.json").then(function(r){return r.json()}).then(function(v){last=v;render()},function(){render()});
  });
}
if(dlg){
  document.addEventListener("click",function(e){
    if(e.defaultPrevented||e.button!==0||e.metaKey||e.ctrlKey||e.shiftKey||e.altKey)return;
    var a=e.target.closest&&e.target.closest("a");
    if(!a)return;
    var h=a.getAttribute("href")||"";
    if(dlg.contains(a)){
      if(a.hasAttribute("data-close")){e.preventDefault();closeDlg()}
      else if(/^\/edit\/port\//.test(h)){e.preventDefault();openPort(h)}
      return;
    }
    if(a.classList.contains("pl")&&/^\/edit\/port\//.test(h)){e.preventDefault();openPort(h)}
  });
  dlg.addEventListener("click",function(e){if(e.target===dlg||e.target.classList.contains("x"))closeDlg()});
  dlg.addEventListener("submit",function(e){
    var f=e.target;e.preventDefault();
    var data=new URLSearchParams(new FormData(f));
    fetch(f.getAttribute("action")+"?fragment=1",{method:"POST",body:data,credentials:"same-origin",
      headers:{"Content-Type":"application/x-www-form-urlencoded"}}).then(function(r){
      if(r.status===204){closeDlg();refreshMap();return}
      return r.text().then(function(t){body().innerHTML=t;var s=$(".err",dlg);if(s)s.scrollIntoView({block:"nearest"})});
    },function(){body().insertAdjacentHTML("afterbegin",'<div class="err">Could not reach the server.</div>')});
  });
}

/* ---------- live status ---------- */
function ago(iso,now){
  var s=Math.max(0,Math.round((now-new Date(iso).getTime())/1000));
  if(s<60)return s+"s";if(s<3600)return Math.floor(s/60)+"m";if(s<86400)return Math.floor(s/3600)+"h";return Math.floor(s/86400)+"d";
}
function setBadge(){
  if(!badge)return;
  var cls="om-badge",txt;
  if(!live){txt="Live off"}
  else if(!last||!last.time||last.time.indexOf("0001")===0){txt="Live · starting"}
  else{
    txt="Live · updated "+ago(last.time,Date.now())+" ago";cls+=" on";
    if(!last.switchConfigured)txt+=" · switch not configured";
    else if(!last.switchOk){txt+=" · switch unreadable";cls="om-badge warn"}
  }
  badge.className=cls;badge.innerHTML="<i></i>";badge.appendChild(document.createTextNode(txt));
}
function dots(){
  var m=main();if(!m||!last)return;
  var by={};(last.ports||[]).forEach(function(p){by[p.n]=p});
  var as=m.querySelectorAll("a.pl");
  for(var i=0;i<as.length;i++){
    var a=as[i],mm=/^\/edit\/port\/(\d+)$/.exec(a.getAttribute("href")||"");
    Array.prototype.slice.call(a.querySelectorAll(".om-ld,.om-ring")).forEach(function(n){n.remove()});
    if(!mm)continue;var p=by[+mm[1]],r=a.querySelector("rect");if(!p||!r)continue;
    var x=+r.getAttribute("x"),y=+r.getAttribute("y"),w=+r.getAttribute("width"),h=+r.getAttribute("height");
    var s=p.status==="connected"?(p.slow?"slow":"up"):(p.status==="disabled"?"disabled":"down");
    var tip="Port "+p.n+": "+p.status+(p.speed?" · "+p.speed:"")+" · VLAN "+p.vlan+(p.slow?" (slower than 1000 Mb/s)":"");
    if(p.mismatch)tip+=" · VLAN does not match the map lane (suggests "+p.suggest+")";
    if(p.macs&&p.macs.length)tip+=" · MACs: "+p.macs.slice(0,4).join(", ")+(p.macs.length>4?" +"+(p.macs.length-4):"");
    if(p.mismatch){var ring=document.createElementNS(NS,"rect");ring.setAttribute("class","om-ring");
      ["x","y","width","height"].forEach(function(k,j){ring.setAttribute(k,[x-2,y-2,w+4,h+4][j])});ring.setAttribute("rx",6);a.appendChild(ring)}
    var c=document.createElementNS(NS,"circle");c.setAttribute("class","om-ld");c.setAttribute("data-s",s);
    c.setAttribute("cx",x+7);c.setAttribute("cy",y+h-7);c.setAttribute("r",4.5);
    var t=document.createElementNS(NS,"title");t.textContent=tip;c.appendChild(t);
    a.appendChild(c);
  }
}
var QUAD=/(^|[^\d.])(\d{1,3}(?:\.\d{1,3}){3})(?!\d)/g;
function pills(){
  var m=main();if(!m||!last)return;
  Array.prototype.slice.call(m.querySelectorAll(".om-pill")).forEach(function(n){n.remove()});
  var dev={};(last.devices||[]).forEach(function(d){dev[d.ip]=d});
  var now=Date.now(),tds=m.querySelectorAll("td");
  for(var i=0;i<tds.length;i++){
    var td=tds[i],txt=td.textContent,seen={},mm;QUAD.lastIndex=0;var found=[];
    while((mm=QUAD.exec(txt))){if(dev[mm[2]]&&!seen[mm[2]]){seen[mm[2]]=1;found.push(dev[mm[2]])}}
    found.forEach(function(d){
      var s=document.createElement("span");s.className="om-pill "+(d.online?"up":"down");
      var lab=found.length>1?d.ip+" ":"";
      s.textContent=d.online?"● "+lab+"online":"○ "+lab+"offline · "+(d.lastSeen?"last seen "+ago(d.lastSeen,now)+" ago":"never seen");
      s.title=d.ip;td.appendChild(s);
    });
  }
}
var KIND={new_mac:"New device",mac_gone:"Device gone",mac_moved:"Moved",link_change:"Link",device_offline:"Offline",device_online:"Back online",vlan_mismatch:"Lane differs"};
function alertsUI(){
  if(!alertsBox)return;
  var list=(last&&last.alerts)||[];
  if(!list.length){alertsBox.hidden=true;alertsBox.innerHTML="";return}
  alertsBox.hidden=false;alertsBox.innerHTML="";
  var h=document.createElement("h2");h.textContent=list.length+(list.length===1?" change":" changes")+" to review";alertsBox.appendChild(h);
  var ul=document.createElement("ul");
  list.forEach(function(a){
    var li=document.createElement("li");
    var k=document.createElement("span");k.className="kind";k.textContent=KIND[a.kind]||a.kind;li.appendChild(k);
    var t=document.createElement("span");t.className="txt";t.textContent=a.details;li.appendChild(t);
    var tm=document.createElement("time");tm.textContent=ago(a.time,Date.now())+" ago";li.appendChild(tm);
    var ac=document.createElement("button");ac.type="button";ac.className="om-btn";ac.textContent="Accept into map";
    ac.onclick=function(){
      if(a.port>0&&dlg){openPort("/edit/port/"+a.port,a.id)}
      else act(a.id,"accept");
    };
    var ig=document.createElement("button");ig.type="button";ig.className="om-btn alt";ig.textContent="Ignore";
    ig.onclick=function(){act(a.id,"ignore")};
    li.appendChild(ac);li.appendChild(ig);ul.appendChild(li);
  });
  alertsBox.appendChild(ul);
}
function act(id,what){
  fetch("/alerts/"+id+"/"+what,{method:"POST",credentials:"same-origin"}).then(function(){
    return fetch("/live.json").then(function(r){return r.json()}).then(function(v){last=v;render()});
  });
}
function render(){setBadge();dots();pills();alertsUI()}
function poll(){
  fetch("/live.json").then(function(r){return r.json()}).then(function(v){last=v;render()}).catch(function(){});
  setTimeout(poll,5000);
}
if(live&&window.EventSource){
  var es=new EventSource("/events");
  es.addEventListener("snapshot",function(e){try{last=JSON.parse(e.data);render()}catch(x){}});
  es.onerror=function(){if(es.readyState===2)poll()};
}else if(live){poll()}
setBadge();
setInterval(setBadge,1000);
})();
