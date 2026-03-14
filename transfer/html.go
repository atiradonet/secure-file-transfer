package main

// decryptTemplate is the self-contained HTML decrypt page.
// Placeholders are substituted by createSecureBundle:
//
//	{{TITLE}}       — filename shown in the page title and heading
//	{{FILENAME_JS}} — JSON-encoded filename passed to the download trigger
//	{{SALT}}        — base64-encoded 16-byte PBKDF2 salt
//	{{NONCE}}       — base64-encoded 12-byte AES-GCM nonce
//	{{DATA}}        — base64-encoded AES-256-GCM ciphertext + tag
//
// The page has no external dependencies and works fully offline.
// Decryption uses the Web Crypto API built into every modern browser.
const decryptTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Decrypt — {{TITLE}}</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
  background:#f0f2f5;
  display:flex;align-items:center;justify-content:center;
  min-height:100vh;padding:1.5rem
}
.card{
  background:#fff;border-radius:10px;
  box-shadow:0 2px 16px rgba(0,0,0,.10);
  padding:2rem 2.25rem;width:100%;max-width:440px
}
.logo{font-size:.75rem;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:#888;margin-bottom:1.25rem}
h1{font-size:1.05rem;font-weight:600;margin-bottom:.3rem}
.fname{font-size:.82rem;color:#666;word-break:break-all;margin-bottom:1.75rem;
  padding:.45rem .6rem;background:#f7f7f7;border-radius:5px;font-family:monospace}
label{font-size:.82rem;font-weight:500;display:block;margin-bottom:.4rem;color:#333}
input[type=password]{
  width:100%;padding:.6rem .75rem;
  border:1.5px solid #ddd;border-radius:6px;
  font-size:.95rem;font-family:monospace;letter-spacing:.06em;
  outline:none;transition:border-color .15s
}
input[type=password]:focus{border-color:#0071e3}
button{
  margin-top:.9rem;width:100%;
  background:#0071e3;color:#fff;border:none;border-radius:6px;
  padding:.65rem 1rem;font-size:.95rem;font-weight:500;cursor:pointer;
  transition:background .15s
}
button:hover{background:#005bbf}
button:disabled{background:#b0b0b0;cursor:default}
.status{margin-top:.85rem;font-size:.82rem;min-height:1.1em;line-height:1.4}
.ok{color:#1a7f37}.err{color:#c0392b}
</style>
</head>
<body>
<div class="card">
  <div class="logo">Secure File Transfer</div>
  <h1>Decrypt file</h1>
  <div class="fname">{{TITLE}}</div>
  <label for="pw">Password</label>
  <input type="password" id="pw" autofocus placeholder="Enter password…">
  <button id="btn" onclick="run()">Decrypt &amp; Download</button>
  <div class="status" id="st"></div>
</div>
<script>
const FILENAME={{FILENAME_JS}};
const SALT="{{SALT}}";
const NONCE="{{NONCE}}";
const DATA="{{DATA}}";

function b64(s){return Uint8Array.from(atob(s),c=>c.charCodeAt(0))}

async function run(){
  const btn=document.getElementById('btn');
  const st=document.getElementById('st');
  const pw=document.getElementById('pw').value;
  if(!pw){st.className='status err';st.textContent='Enter the password.';return;}
  btn.disabled=true;
  st.className='status';st.textContent='Decrypting\u2026';
  try{
    const km=await crypto.subtle.importKey(
      'raw',new TextEncoder().encode(pw),'PBKDF2',false,['deriveKey']);
    const key=await crypto.subtle.deriveKey(
      {name:'PBKDF2',salt:b64(SALT),iterations:100000,hash:'SHA-256'},
      km,{name:'AES-GCM',length:256},false,['decrypt']);
    const plain=await crypto.subtle.decrypt(
      {name:'AES-GCM',iv:b64(NONCE)},key,b64(DATA));
    const a=document.createElement('a');
    a.href=URL.createObjectURL(new Blob([plain]));
    a.download=FILENAME;
    a.click();
    st.className='status ok';
    st.textContent='\u2713 Decrypted \u2014 check your Downloads folder.';
  }catch(e){
    st.className='status err';
    st.textContent='Wrong password or corrupted file.';
    btn.disabled=false;
  }
}
document.getElementById('pw').addEventListener('keydown',e=>{if(e.key==='Enter')run();});
</script>
</body>
</html>
`
