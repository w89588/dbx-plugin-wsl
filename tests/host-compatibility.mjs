// Runs the unmodified, pinned upstream Rust manifest parsers against our real
// package. This is host-rule validation, not a desktop installation/UI test.
// Prerequisites: gh, cargo, python. Downloads only public upstream source/crates.
import assert from 'node:assert/strict';
import {readFileSync,writeFileSync,mkdirSync,existsSync} from 'node:fs';
import {resolve,join} from 'node:path';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
const run=(cmd,args,options={})=>execFileSync(cmd,args,{windowsHide:true,encoding:'utf8',...options});
const manifest=JSON.parse(readFileSync('manifest.json','utf8'));
assert(!Object.hasOwn(manifest.engines,'dbx'),'Do not restore an arbitrary DBX product-version gate');
const versions=JSON.parse(readFileSync('tests/fixtures/host-compatibility.json','utf8'));
const root=resolve('tests/tmp/host-compatibility');mkdirSync(join(root,'src'),{recursive:true});
for(const [i,v] of versions.entries()){
  const path=join(root,'src',`host${i}.rs`);
  if(!existsSync(path))writeFileSync(path,run('gh',['api','-H','Accept: application/vnd.github.raw+json',`repos/t8y2/dbx/contents/crates/dbx-plugin-runtime/src/plugins/manifest.rs?ref=${v.commit}`]));
  assert.equal(createHash('sha256').update(readFileSync(path)).digest('hex'),v.sha256,`Upstream ${v.version} source changed`);
  if(v.bridgeSha256){
    const bridge=join(root,`bridge-${v.version}.ts`);
    if(!existsSync(bridge))writeFileSync(bridge,run('gh',['api','-H','Accept: application/vnd.github.raw+json',`repos/t8y2/dbx/contents/apps/desktop/src/lib/plugins/pluginHostBridge.ts?ref=${v.commit}`]));
    assert.equal(createHash('sha256').update(readFileSync(bridge)).digest('hex'),v.bridgeSha256,'Historical SDK source changed');
  }
}
writeFileSync(join(root,'Cargo.toml'),`[package]
name = "dbx-host-compatibility"
version = "0.1.0"
edition = "2021"
[dependencies]
serde = { version = "1", features = ["derive"] }
serde_json = "1"
semver = "1"
`);
writeFileSync(join(root,'src/main.rs'),`#![allow(dead_code)]
mod host0; mod host1; mod host2;
fn main() {
 let dir=std::path::PathBuf::from(std::env::args().nth(1).unwrap());
 let raw=std::fs::read_to_string(dir.join("manifest.json")).unwrap();
 let old=serde_json::from_str::<host0::PluginManifest>(&raw);
 assert!(old.is_err(), "0.6.26 should reject the unsupported MCP declaration");
 let error=old.unwrap_err().to_string(); assert!(error.contains("mcp"), "{error}");
 println!("PASS 0.6.26 boundary: {error}");
 let current:host1::PluginManifest=serde_json::from_str(&raw).unwrap();
 let result=current.compatibility(&dir,"0.6.27");
 assert!(result.compatible,"0.6.27: {:?}",result.errors);
 assert!(!current.external_tools_excluded());
 println!("PASS 0.6.27 actual host parser and compatibility rules: package entrypoints, permissions, contributions, Host API; MCP enabled");
 let mut control:serde_json::Value=serde_json::from_str(&raw).unwrap();
 control["engines"]["dbx"]=serde_json::json!(">=0.6.55");
 let prior:host1::PluginManifest=serde_json::from_value(control).unwrap();
 let blocked=prior.compatibility(&dir,"0.6.27");
 assert!(!blocked.compatible && blocked.errors.iter().any(|e|e.contains("requires DBX")));
 println!("PASS control: previous >=0.6.55 gate rejects an otherwise compatible 0.6.27 host");
 let latest:host2::PluginManifest=serde_json::from_str(&raw).unwrap();
 let result=latest.compatibility(&dir,"0.6.55");
 assert!(result.compatible,"0.6.55: {:?}",result.errors);
 assert!(!latest.external_tools_excluded());
 println!("PASS 0.6.55 actual host parser and compatibility rules; MCP enabled");
}
`);
const pkg=resolve(`dist/com.dbx.wsl-${manifest.version}-windows-x64.dbxp`);
const unpack=join(root,'package-'+manifest.version);
run('python',['-c','import zipfile,sys; z=zipfile.ZipFile(sys.argv[1]); assert z.testzip() is None; z.extractall(sys.argv[2])',pkg,unpack]);
run('cargo',['run','--quiet',...(process.env.DBX_COMPAT_TARGET?['--target',process.env.DBX_COMPAT_TARGET]:[]),'--manifest-path',join(root,'Cargo.toml'),'--',unpack],{stdio:'inherit'});
