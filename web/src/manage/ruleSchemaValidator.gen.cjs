"use strict";
exports["https://gallery.local/schemas/rule-package-v1.json"] = validate20;
const schema31 = {"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://gallery.local/schemas/rule-package-v1.json","title":"Gallery Canonical Rule Package v1","type":"object","additionalProperties":false,"required":["rule_set_id","version","schema_version","normalization_algorithm_version","compiler_requirement","cel_profile_version","parameter_schema","provider_namespaces","primitives","cel_expressions","tests","extensions"],"properties":{"rule_set_id":{"type":"string","pattern":"^rset_[0-9a-f-]{36}$"},"version":{"type":"string","pattern":"^[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$"},"schema_version":{"const":1,"default":1},"normalization_algorithm_version":{"const":"gallery-canonical-json-v1","default":"gallery-canonical-json-v1"},"compiler_requirement":{"type":"string","const":"gallery-rule-compiler-v1","default":"gallery-rule-compiler-v1"},"cel_profile_version":{"const":"gallery-cel-v1","default":"gallery-cel-v1"},"parameter_schema":{"type":"object","default":{"type":"object","additionalProperties":false}},"provider_namespaces":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"uniqueItems":true,"default":[]},"primitives":{"type":"array","maxItems":4096,"items":{"type":"object","additionalProperties":false,"required":["id","kind","config"],"properties":{"id":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_.-]{0,127}$"},"kind":{"type":"string","enum":["path_match","selector","fallback","stable_key","media_classify","media_hidden","media_order","cover_candidate","cover_disable_marker","badge","work_date","path_capture","presentation","metadata_map","condition"]},"config":{"type":"object"}}},"default":[]},"cel_expressions":{"type":"array","maxItems":1024,"items":{"type":"object","additionalProperties":false,"required":["id","purpose","expression"],"properties":{"id":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_.-]{0,127}$"},"purpose":{"type":"string","enum":["predicate","scalar"]},"expression":{"type":"string","maxLength":4096}}},"default":[]},"tests":{"type":"array","minItems":1,"maxItems":10000},"extensions":{"type":"object","$comment":"分类 extension 为对象 {required:bool, semantic:bool, version?:string, payload?:any}；required 或 semantic 者必须落在编译器支持的 namespace/version 内，semantic 者参与 semantic_hash。缺少 semantic 字段的遗留 extension 按 optional+nonsemantic 处理，只参与 package_hash。分类语义由编译器强制，本 Schema 保持宽松以兼容既有 RuleVersion。","propertyNames":{"pattern":"^[a-z0-9][a-z0-9-]{0,62}(?:\\.[a-z0-9][a-z0-9-]{0,62})+$"},"additionalProperties":true,"default":{}},"ui_metadata":{"type":"object","description":"仅供 Schema 表单和编辑器使用的非执行元数据","properties":{"title":{"type":"string","maxLength":512},"description":{"type":"string","maxLength":4096},"order":{"type":"array","items":{"type":"string"},"uniqueItems":true},"groups":{"type":"object","additionalProperties":{"type":"object","properties":{"title":{"type":"string"},"description":{"type":"string"},"order":{"type":"integer"}},"additionalProperties":true}},"fields":{"type":"object","description":"按 JSON Pointer 索引的表单字段元数据","additionalProperties":{"type":"object","properties":{"title":{"type":"string"},"description":{"type":"string"},"group":{"type":"string"},"order":{"type":"integer"},"advanced":{"type":"boolean"},"sensitive":{"type":"boolean"},"readOnly":{"type":"boolean"},"computed":{"type":"boolean"},"default":{},"enum":{"type":"array"},"visibleWhen":{"type":"object","additionalProperties":true},"items":{"type":"object","additionalProperties":true}},"additionalProperties":true}}},"additionalProperties":true},"package_hash":{"type":"string","pattern":"^[0-9a-f]{64}$"},"semantic_hash":{"type":"string","pattern":"^[0-9a-f]{64}$"}}};
const func1 = Object.prototype.hasOwnProperty;
const func2 = require("ajv/dist/runtime/ucs2length").default;
const pattern4 = new RegExp("^rset_[0-9a-f-]{36}$", "u");
const pattern5 = new RegExp("^[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$", "u");
const pattern6 = new RegExp("^[A-Za-z][A-Za-z0-9_.-]{0,127}$", "u");
const pattern8 = new RegExp("^[a-z0-9][a-z0-9-]{0,62}(?:\\.[a-z0-9][a-z0-9-]{0,62})+$", "u");
const pattern9 = new RegExp("^[0-9a-f]{64}$", "u");

function validate20(data, {instancePath="", parentData, parentDataProperty, rootData=data, dynamicAnchors={}}={}){
/*# sourceURL="https://gallery.local/schemas/rule-package-v1.json" */;
let vErrors = null;
let errors = 0;
const evaluated0 = validate20.evaluated;
if(evaluated0.dynamicProps){
evaluated0.props = undefined;
}
if(evaluated0.dynamicItems){
evaluated0.items = undefined;
}
if(data && typeof data == "object" && !Array.isArray(data)){
if(data.rule_set_id === undefined){
const err0 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "rule_set_id"},message:"must have required property '"+"rule_set_id"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err0];
}
else {
vErrors.push(err0);
}
errors++;
}
if(data.version === undefined){
const err1 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "version"},message:"must have required property '"+"version"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err1];
}
else {
vErrors.push(err1);
}
errors++;
}
if(data.schema_version === undefined){
const err2 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "schema_version"},message:"must have required property '"+"schema_version"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err2];
}
else {
vErrors.push(err2);
}
errors++;
}
if(data.normalization_algorithm_version === undefined){
const err3 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "normalization_algorithm_version"},message:"must have required property '"+"normalization_algorithm_version"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err3];
}
else {
vErrors.push(err3);
}
errors++;
}
if(data.compiler_requirement === undefined){
const err4 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "compiler_requirement"},message:"must have required property '"+"compiler_requirement"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err4];
}
else {
vErrors.push(err4);
}
errors++;
}
if(data.cel_profile_version === undefined){
const err5 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "cel_profile_version"},message:"must have required property '"+"cel_profile_version"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err5];
}
else {
vErrors.push(err5);
}
errors++;
}
if(data.parameter_schema === undefined){
const err6 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "parameter_schema"},message:"must have required property '"+"parameter_schema"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err6];
}
else {
vErrors.push(err6);
}
errors++;
}
if(data.provider_namespaces === undefined){
const err7 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "provider_namespaces"},message:"must have required property '"+"provider_namespaces"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err7];
}
else {
vErrors.push(err7);
}
errors++;
}
if(data.primitives === undefined){
const err8 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "primitives"},message:"must have required property '"+"primitives"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err8];
}
else {
vErrors.push(err8);
}
errors++;
}
if(data.cel_expressions === undefined){
const err9 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "cel_expressions"},message:"must have required property '"+"cel_expressions"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err9];
}
else {
vErrors.push(err9);
}
errors++;
}
if(data.tests === undefined){
const err10 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "tests"},message:"must have required property '"+"tests"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err10];
}
else {
vErrors.push(err10);
}
errors++;
}
if(data.extensions === undefined){
const err11 = {instancePath,schemaPath:"#/required",keyword:"required",params:{missingProperty: "extensions"},message:"must have required property '"+"extensions"+"'",schema:schema31.required,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err11];
}
else {
vErrors.push(err11);
}
errors++;
}
for(const key0 in data){
if(!(func1.call(schema31.properties, key0))){
const err12 = {instancePath,schemaPath:"#/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key0},message:"must NOT have additional properties",schema:false,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err12];
}
else {
vErrors.push(err12);
}
errors++;
}
}
if(data.rule_set_id !== undefined){
let data0 = data.rule_set_id;
if(typeof data0 === "string"){
if(!pattern4.test(data0)){
const err13 = {instancePath:instancePath+"/rule_set_id",schemaPath:"#/properties/rule_set_id/pattern",keyword:"pattern",params:{pattern: "^rset_[0-9a-f-]{36}$"},message:"must match pattern \""+"^rset_[0-9a-f-]{36}$"+"\"",schema:"^rset_[0-9a-f-]{36}$",parentSchema:schema31.properties.rule_set_id,data:data0};
if(vErrors === null){
vErrors = [err13];
}
else {
vErrors.push(err13);
}
errors++;
}
}
else {
const err14 = {instancePath:instancePath+"/rule_set_id",schemaPath:"#/properties/rule_set_id/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.rule_set_id.type,parentSchema:schema31.properties.rule_set_id,data:data0};
if(vErrors === null){
vErrors = [err14];
}
else {
vErrors.push(err14);
}
errors++;
}
}
if(data.version !== undefined){
let data1 = data.version;
if(typeof data1 === "string"){
if(!pattern5.test(data1)){
const err15 = {instancePath:instancePath+"/version",schemaPath:"#/properties/version/pattern",keyword:"pattern",params:{pattern: "^[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$"},message:"must match pattern \""+"^[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$"+"\"",schema:"^[0-9]+\\.[0-9]+\\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$",parentSchema:schema31.properties.version,data:data1};
if(vErrors === null){
vErrors = [err15];
}
else {
vErrors.push(err15);
}
errors++;
}
}
else {
const err16 = {instancePath:instancePath+"/version",schemaPath:"#/properties/version/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.version.type,parentSchema:schema31.properties.version,data:data1};
if(vErrors === null){
vErrors = [err16];
}
else {
vErrors.push(err16);
}
errors++;
}
}
if(data.schema_version !== undefined){
let data2 = data.schema_version;
if(1 !== data2){
const err17 = {instancePath:instancePath+"/schema_version",schemaPath:"#/properties/schema_version/const",keyword:"const",params:{allowedValue: 1},message:"must be equal to constant",schema:1,parentSchema:schema31.properties.schema_version,data:data2};
if(vErrors === null){
vErrors = [err17];
}
else {
vErrors.push(err17);
}
errors++;
}
}
if(data.normalization_algorithm_version !== undefined){
let data3 = data.normalization_algorithm_version;
if("gallery-canonical-json-v1" !== data3){
const err18 = {instancePath:instancePath+"/normalization_algorithm_version",schemaPath:"#/properties/normalization_algorithm_version/const",keyword:"const",params:{allowedValue: "gallery-canonical-json-v1"},message:"must be equal to constant",schema:"gallery-canonical-json-v1",parentSchema:schema31.properties.normalization_algorithm_version,data:data3};
if(vErrors === null){
vErrors = [err18];
}
else {
vErrors.push(err18);
}
errors++;
}
}
if(data.compiler_requirement !== undefined){
let data4 = data.compiler_requirement;
if(typeof data4 !== "string"){
const err19 = {instancePath:instancePath+"/compiler_requirement",schemaPath:"#/properties/compiler_requirement/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.compiler_requirement.type,parentSchema:schema31.properties.compiler_requirement,data:data4};
if(vErrors === null){
vErrors = [err19];
}
else {
vErrors.push(err19);
}
errors++;
}
if("gallery-rule-compiler-v1" !== data4){
const err20 = {instancePath:instancePath+"/compiler_requirement",schemaPath:"#/properties/compiler_requirement/const",keyword:"const",params:{allowedValue: "gallery-rule-compiler-v1"},message:"must be equal to constant",schema:"gallery-rule-compiler-v1",parentSchema:schema31.properties.compiler_requirement,data:data4};
if(vErrors === null){
vErrors = [err20];
}
else {
vErrors.push(err20);
}
errors++;
}
}
if(data.cel_profile_version !== undefined){
let data5 = data.cel_profile_version;
if("gallery-cel-v1" !== data5){
const err21 = {instancePath:instancePath+"/cel_profile_version",schemaPath:"#/properties/cel_profile_version/const",keyword:"const",params:{allowedValue: "gallery-cel-v1"},message:"must be equal to constant",schema:"gallery-cel-v1",parentSchema:schema31.properties.cel_profile_version,data:data5};
if(vErrors === null){
vErrors = [err21];
}
else {
vErrors.push(err21);
}
errors++;
}
}
if(data.parameter_schema !== undefined){
let data6 = data.parameter_schema;
if(!(data6 && typeof data6 == "object" && !Array.isArray(data6))){
const err22 = {instancePath:instancePath+"/parameter_schema",schemaPath:"#/properties/parameter_schema/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.parameter_schema.type,parentSchema:schema31.properties.parameter_schema,data:data6};
if(vErrors === null){
vErrors = [err22];
}
else {
vErrors.push(err22);
}
errors++;
}
}
if(data.provider_namespaces !== undefined){
let data7 = data.provider_namespaces;
if(Array.isArray(data7)){
const len0 = data7.length;
for(let i0=0; i0<len0; i0++){
let data8 = data7[i0];
if(typeof data8 === "string"){
if(func2(data8) > 128){
const err23 = {instancePath:instancePath+"/provider_namespaces/" + i0,schemaPath:"#/properties/provider_namespaces/items/maxLength",keyword:"maxLength",params:{limit: 128},message:"must NOT have more than 128 characters",schema:128,parentSchema:schema31.properties.provider_namespaces.items,data:data8};
if(vErrors === null){
vErrors = [err23];
}
else {
vErrors.push(err23);
}
errors++;
}
if(func2(data8) < 1){
const err24 = {instancePath:instancePath+"/provider_namespaces/" + i0,schemaPath:"#/properties/provider_namespaces/items/minLength",keyword:"minLength",params:{limit: 1},message:"must NOT have fewer than 1 characters",schema:1,parentSchema:schema31.properties.provider_namespaces.items,data:data8};
if(vErrors === null){
vErrors = [err24];
}
else {
vErrors.push(err24);
}
errors++;
}
}
else {
const err25 = {instancePath:instancePath+"/provider_namespaces/" + i0,schemaPath:"#/properties/provider_namespaces/items/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.provider_namespaces.items.type,parentSchema:schema31.properties.provider_namespaces.items,data:data8};
if(vErrors === null){
vErrors = [err25];
}
else {
vErrors.push(err25);
}
errors++;
}
}
let i1 = data7.length;
let j0;
if(i1 > 1){
const indices0 = {};
for(;i1--;){
let item0 = data7[i1];
if(typeof item0 !== "string"){
continue;
}
if(typeof indices0[item0] == "number"){
j0 = indices0[item0];
const err26 = {instancePath:instancePath+"/provider_namespaces",schemaPath:"#/properties/provider_namespaces/uniqueItems",keyword:"uniqueItems",params:{i: i1, j: j0},message:"must NOT have duplicate items (items ## "+j0+" and "+i1+" are identical)",schema:true,parentSchema:schema31.properties.provider_namespaces,data:data7};
if(vErrors === null){
vErrors = [err26];
}
else {
vErrors.push(err26);
}
errors++;
break;
}
indices0[item0] = i1;
}
}
}
else {
const err27 = {instancePath:instancePath+"/provider_namespaces",schemaPath:"#/properties/provider_namespaces/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.provider_namespaces.type,parentSchema:schema31.properties.provider_namespaces,data:data7};
if(vErrors === null){
vErrors = [err27];
}
else {
vErrors.push(err27);
}
errors++;
}
}
if(data.primitives !== undefined){
let data9 = data.primitives;
if(Array.isArray(data9)){
if(data9.length > 4096){
const err28 = {instancePath:instancePath+"/primitives",schemaPath:"#/properties/primitives/maxItems",keyword:"maxItems",params:{limit: 4096},message:"must NOT have more than 4096 items",schema:4096,parentSchema:schema31.properties.primitives,data:data9};
if(vErrors === null){
vErrors = [err28];
}
else {
vErrors.push(err28);
}
errors++;
}
const len1 = data9.length;
for(let i2=0; i2<len1; i2++){
let data10 = data9[i2];
if(data10 && typeof data10 == "object" && !Array.isArray(data10)){
if(data10.id === undefined){
const err29 = {instancePath:instancePath+"/primitives/" + i2,schemaPath:"#/properties/primitives/items/required",keyword:"required",params:{missingProperty: "id"},message:"must have required property '"+"id"+"'",schema:schema31.properties.primitives.items.required,parentSchema:schema31.properties.primitives.items,data:data10};
if(vErrors === null){
vErrors = [err29];
}
else {
vErrors.push(err29);
}
errors++;
}
if(data10.kind === undefined){
const err30 = {instancePath:instancePath+"/primitives/" + i2,schemaPath:"#/properties/primitives/items/required",keyword:"required",params:{missingProperty: "kind"},message:"must have required property '"+"kind"+"'",schema:schema31.properties.primitives.items.required,parentSchema:schema31.properties.primitives.items,data:data10};
if(vErrors === null){
vErrors = [err30];
}
else {
vErrors.push(err30);
}
errors++;
}
if(data10.config === undefined){
const err31 = {instancePath:instancePath+"/primitives/" + i2,schemaPath:"#/properties/primitives/items/required",keyword:"required",params:{missingProperty: "config"},message:"must have required property '"+"config"+"'",schema:schema31.properties.primitives.items.required,parentSchema:schema31.properties.primitives.items,data:data10};
if(vErrors === null){
vErrors = [err31];
}
else {
vErrors.push(err31);
}
errors++;
}
for(const key1 in data10){
if(!(((key1 === "id") || (key1 === "kind")) || (key1 === "config"))){
const err32 = {instancePath:instancePath+"/primitives/" + i2,schemaPath:"#/properties/primitives/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key1},message:"must NOT have additional properties",schema:false,parentSchema:schema31.properties.primitives.items,data:data10};
if(vErrors === null){
vErrors = [err32];
}
else {
vErrors.push(err32);
}
errors++;
}
}
if(data10.id !== undefined){
let data11 = data10.id;
if(typeof data11 === "string"){
if(!pattern6.test(data11)){
const err33 = {instancePath:instancePath+"/primitives/" + i2+"/id",schemaPath:"#/properties/primitives/items/properties/id/pattern",keyword:"pattern",params:{pattern: "^[A-Za-z][A-Za-z0-9_.-]{0,127}$"},message:"must match pattern \""+"^[A-Za-z][A-Za-z0-9_.-]{0,127}$"+"\"",schema:"^[A-Za-z][A-Za-z0-9_.-]{0,127}$",parentSchema:schema31.properties.primitives.items.properties.id,data:data11};
if(vErrors === null){
vErrors = [err33];
}
else {
vErrors.push(err33);
}
errors++;
}
}
else {
const err34 = {instancePath:instancePath+"/primitives/" + i2+"/id",schemaPath:"#/properties/primitives/items/properties/id/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.primitives.items.properties.id.type,parentSchema:schema31.properties.primitives.items.properties.id,data:data11};
if(vErrors === null){
vErrors = [err34];
}
else {
vErrors.push(err34);
}
errors++;
}
}
if(data10.kind !== undefined){
let data12 = data10.kind;
if(typeof data12 !== "string"){
const err35 = {instancePath:instancePath+"/primitives/" + i2+"/kind",schemaPath:"#/properties/primitives/items/properties/kind/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.primitives.items.properties.kind.type,parentSchema:schema31.properties.primitives.items.properties.kind,data:data12};
if(vErrors === null){
vErrors = [err35];
}
else {
vErrors.push(err35);
}
errors++;
}
if(!(((((((((((((((data12 === "path_match") || (data12 === "selector")) || (data12 === "fallback")) || (data12 === "stable_key")) || (data12 === "media_classify")) || (data12 === "media_hidden")) || (data12 === "media_order")) || (data12 === "cover_candidate")) || (data12 === "cover_disable_marker")) || (data12 === "badge")) || (data12 === "work_date")) || (data12 === "path_capture")) || (data12 === "presentation")) || (data12 === "metadata_map")) || (data12 === "condition"))){
const err36 = {instancePath:instancePath+"/primitives/" + i2+"/kind",schemaPath:"#/properties/primitives/items/properties/kind/enum",keyword:"enum",params:{allowedValues: schema31.properties.primitives.items.properties.kind.enum},message:"must be equal to one of the allowed values",schema:schema31.properties.primitives.items.properties.kind.enum,parentSchema:schema31.properties.primitives.items.properties.kind,data:data12};
if(vErrors === null){
vErrors = [err36];
}
else {
vErrors.push(err36);
}
errors++;
}
}
if(data10.config !== undefined){
let data13 = data10.config;
if(!(data13 && typeof data13 == "object" && !Array.isArray(data13))){
const err37 = {instancePath:instancePath+"/primitives/" + i2+"/config",schemaPath:"#/properties/primitives/items/properties/config/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.primitives.items.properties.config.type,parentSchema:schema31.properties.primitives.items.properties.config,data:data13};
if(vErrors === null){
vErrors = [err37];
}
else {
vErrors.push(err37);
}
errors++;
}
}
}
else {
const err38 = {instancePath:instancePath+"/primitives/" + i2,schemaPath:"#/properties/primitives/items/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.primitives.items.type,parentSchema:schema31.properties.primitives.items,data:data10};
if(vErrors === null){
vErrors = [err38];
}
else {
vErrors.push(err38);
}
errors++;
}
}
}
else {
const err39 = {instancePath:instancePath+"/primitives",schemaPath:"#/properties/primitives/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.primitives.type,parentSchema:schema31.properties.primitives,data:data9};
if(vErrors === null){
vErrors = [err39];
}
else {
vErrors.push(err39);
}
errors++;
}
}
if(data.cel_expressions !== undefined){
let data14 = data.cel_expressions;
if(Array.isArray(data14)){
if(data14.length > 1024){
const err40 = {instancePath:instancePath+"/cel_expressions",schemaPath:"#/properties/cel_expressions/maxItems",keyword:"maxItems",params:{limit: 1024},message:"must NOT have more than 1024 items",schema:1024,parentSchema:schema31.properties.cel_expressions,data:data14};
if(vErrors === null){
vErrors = [err40];
}
else {
vErrors.push(err40);
}
errors++;
}
const len2 = data14.length;
for(let i3=0; i3<len2; i3++){
let data15 = data14[i3];
if(data15 && typeof data15 == "object" && !Array.isArray(data15)){
if(data15.id === undefined){
const err41 = {instancePath:instancePath+"/cel_expressions/" + i3,schemaPath:"#/properties/cel_expressions/items/required",keyword:"required",params:{missingProperty: "id"},message:"must have required property '"+"id"+"'",schema:schema31.properties.cel_expressions.items.required,parentSchema:schema31.properties.cel_expressions.items,data:data15};
if(vErrors === null){
vErrors = [err41];
}
else {
vErrors.push(err41);
}
errors++;
}
if(data15.purpose === undefined){
const err42 = {instancePath:instancePath+"/cel_expressions/" + i3,schemaPath:"#/properties/cel_expressions/items/required",keyword:"required",params:{missingProperty: "purpose"},message:"must have required property '"+"purpose"+"'",schema:schema31.properties.cel_expressions.items.required,parentSchema:schema31.properties.cel_expressions.items,data:data15};
if(vErrors === null){
vErrors = [err42];
}
else {
vErrors.push(err42);
}
errors++;
}
if(data15.expression === undefined){
const err43 = {instancePath:instancePath+"/cel_expressions/" + i3,schemaPath:"#/properties/cel_expressions/items/required",keyword:"required",params:{missingProperty: "expression"},message:"must have required property '"+"expression"+"'",schema:schema31.properties.cel_expressions.items.required,parentSchema:schema31.properties.cel_expressions.items,data:data15};
if(vErrors === null){
vErrors = [err43];
}
else {
vErrors.push(err43);
}
errors++;
}
for(const key2 in data15){
if(!(((key2 === "id") || (key2 === "purpose")) || (key2 === "expression"))){
const err44 = {instancePath:instancePath+"/cel_expressions/" + i3,schemaPath:"#/properties/cel_expressions/items/additionalProperties",keyword:"additionalProperties",params:{additionalProperty: key2},message:"must NOT have additional properties",schema:false,parentSchema:schema31.properties.cel_expressions.items,data:data15};
if(vErrors === null){
vErrors = [err44];
}
else {
vErrors.push(err44);
}
errors++;
}
}
if(data15.id !== undefined){
let data16 = data15.id;
if(typeof data16 === "string"){
if(!pattern6.test(data16)){
const err45 = {instancePath:instancePath+"/cel_expressions/" + i3+"/id",schemaPath:"#/properties/cel_expressions/items/properties/id/pattern",keyword:"pattern",params:{pattern: "^[A-Za-z][A-Za-z0-9_.-]{0,127}$"},message:"must match pattern \""+"^[A-Za-z][A-Za-z0-9_.-]{0,127}$"+"\"",schema:"^[A-Za-z][A-Za-z0-9_.-]{0,127}$",parentSchema:schema31.properties.cel_expressions.items.properties.id,data:data16};
if(vErrors === null){
vErrors = [err45];
}
else {
vErrors.push(err45);
}
errors++;
}
}
else {
const err46 = {instancePath:instancePath+"/cel_expressions/" + i3+"/id",schemaPath:"#/properties/cel_expressions/items/properties/id/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.cel_expressions.items.properties.id.type,parentSchema:schema31.properties.cel_expressions.items.properties.id,data:data16};
if(vErrors === null){
vErrors = [err46];
}
else {
vErrors.push(err46);
}
errors++;
}
}
if(data15.purpose !== undefined){
let data17 = data15.purpose;
if(typeof data17 !== "string"){
const err47 = {instancePath:instancePath+"/cel_expressions/" + i3+"/purpose",schemaPath:"#/properties/cel_expressions/items/properties/purpose/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.cel_expressions.items.properties.purpose.type,parentSchema:schema31.properties.cel_expressions.items.properties.purpose,data:data17};
if(vErrors === null){
vErrors = [err47];
}
else {
vErrors.push(err47);
}
errors++;
}
if(!((data17 === "predicate") || (data17 === "scalar"))){
const err48 = {instancePath:instancePath+"/cel_expressions/" + i3+"/purpose",schemaPath:"#/properties/cel_expressions/items/properties/purpose/enum",keyword:"enum",params:{allowedValues: schema31.properties.cel_expressions.items.properties.purpose.enum},message:"must be equal to one of the allowed values",schema:schema31.properties.cel_expressions.items.properties.purpose.enum,parentSchema:schema31.properties.cel_expressions.items.properties.purpose,data:data17};
if(vErrors === null){
vErrors = [err48];
}
else {
vErrors.push(err48);
}
errors++;
}
}
if(data15.expression !== undefined){
let data18 = data15.expression;
if(typeof data18 === "string"){
if(func2(data18) > 4096){
const err49 = {instancePath:instancePath+"/cel_expressions/" + i3+"/expression",schemaPath:"#/properties/cel_expressions/items/properties/expression/maxLength",keyword:"maxLength",params:{limit: 4096},message:"must NOT have more than 4096 characters",schema:4096,parentSchema:schema31.properties.cel_expressions.items.properties.expression,data:data18};
if(vErrors === null){
vErrors = [err49];
}
else {
vErrors.push(err49);
}
errors++;
}
}
else {
const err50 = {instancePath:instancePath+"/cel_expressions/" + i3+"/expression",schemaPath:"#/properties/cel_expressions/items/properties/expression/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.cel_expressions.items.properties.expression.type,parentSchema:schema31.properties.cel_expressions.items.properties.expression,data:data18};
if(vErrors === null){
vErrors = [err50];
}
else {
vErrors.push(err50);
}
errors++;
}
}
}
else {
const err51 = {instancePath:instancePath+"/cel_expressions/" + i3,schemaPath:"#/properties/cel_expressions/items/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.cel_expressions.items.type,parentSchema:schema31.properties.cel_expressions.items,data:data15};
if(vErrors === null){
vErrors = [err51];
}
else {
vErrors.push(err51);
}
errors++;
}
}
}
else {
const err52 = {instancePath:instancePath+"/cel_expressions",schemaPath:"#/properties/cel_expressions/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.cel_expressions.type,parentSchema:schema31.properties.cel_expressions,data:data14};
if(vErrors === null){
vErrors = [err52];
}
else {
vErrors.push(err52);
}
errors++;
}
}
if(data.tests !== undefined){
let data19 = data.tests;
if(Array.isArray(data19)){
if(data19.length > 10000){
const err53 = {instancePath:instancePath+"/tests",schemaPath:"#/properties/tests/maxItems",keyword:"maxItems",params:{limit: 10000},message:"must NOT have more than 10000 items",schema:10000,parentSchema:schema31.properties.tests,data:data19};
if(vErrors === null){
vErrors = [err53];
}
else {
vErrors.push(err53);
}
errors++;
}
if(data19.length < 1){
const err54 = {instancePath:instancePath+"/tests",schemaPath:"#/properties/tests/minItems",keyword:"minItems",params:{limit: 1},message:"must NOT have fewer than 1 items",schema:1,parentSchema:schema31.properties.tests,data:data19};
if(vErrors === null){
vErrors = [err54];
}
else {
vErrors.push(err54);
}
errors++;
}
}
else {
const err55 = {instancePath:instancePath+"/tests",schemaPath:"#/properties/tests/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.tests.type,parentSchema:schema31.properties.tests,data:data19};
if(vErrors === null){
vErrors = [err55];
}
else {
vErrors.push(err55);
}
errors++;
}
}
if(data.extensions !== undefined){
let data20 = data.extensions;
if(data20 && typeof data20 == "object" && !Array.isArray(data20)){
for(const key3 in data20){
const _errs44 = errors;
if(typeof key3 === "string"){
if(!pattern8.test(key3)){
const err56 = {instancePath:instancePath+"/extensions",schemaPath:"#/properties/extensions/propertyNames/pattern",keyword:"pattern",params:{pattern: "^[a-z0-9][a-z0-9-]{0,62}(?:\\.[a-z0-9][a-z0-9-]{0,62})+$"},message:"must match pattern \""+"^[a-z0-9][a-z0-9-]{0,62}(?:\\.[a-z0-9][a-z0-9-]{0,62})+$"+"\"",schema:"^[a-z0-9][a-z0-9-]{0,62}(?:\\.[a-z0-9][a-z0-9-]{0,62})+$",parentSchema:schema31.properties.extensions.propertyNames,data:key3,propertyName:key3};
if(vErrors === null){
vErrors = [err56];
}
else {
vErrors.push(err56);
}
errors++;
}
}
var valid10 = _errs44 === errors;
if(!valid10){
const err57 = {instancePath:instancePath+"/extensions",schemaPath:"#/properties/extensions/propertyNames",keyword:"propertyNames",params:{propertyName: key3},message:"property name must be valid",schema:schema31.properties.extensions.propertyNames,parentSchema:schema31.properties.extensions,data:data20};
if(vErrors === null){
vErrors = [err57];
}
else {
vErrors.push(err57);
}
errors++;
}
}
}
else {
const err58 = {instancePath:instancePath+"/extensions",schemaPath:"#/properties/extensions/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.extensions.type,parentSchema:schema31.properties.extensions,data:data20};
if(vErrors === null){
vErrors = [err58];
}
else {
vErrors.push(err58);
}
errors++;
}
}
if(data.ui_metadata !== undefined){
let data21 = data.ui_metadata;
if(data21 && typeof data21 == "object" && !Array.isArray(data21)){
if(data21.title !== undefined){
let data22 = data21.title;
if(typeof data22 === "string"){
if(func2(data22) > 512){
const err59 = {instancePath:instancePath+"/ui_metadata/title",schemaPath:"#/properties/ui_metadata/properties/title/maxLength",keyword:"maxLength",params:{limit: 512},message:"must NOT have more than 512 characters",schema:512,parentSchema:schema31.properties.ui_metadata.properties.title,data:data22};
if(vErrors === null){
vErrors = [err59];
}
else {
vErrors.push(err59);
}
errors++;
}
}
else {
const err60 = {instancePath:instancePath+"/ui_metadata/title",schemaPath:"#/properties/ui_metadata/properties/title/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.title.type,parentSchema:schema31.properties.ui_metadata.properties.title,data:data22};
if(vErrors === null){
vErrors = [err60];
}
else {
vErrors.push(err60);
}
errors++;
}
}
if(data21.description !== undefined){
let data23 = data21.description;
if(typeof data23 === "string"){
if(func2(data23) > 4096){
const err61 = {instancePath:instancePath+"/ui_metadata/description",schemaPath:"#/properties/ui_metadata/properties/description/maxLength",keyword:"maxLength",params:{limit: 4096},message:"must NOT have more than 4096 characters",schema:4096,parentSchema:schema31.properties.ui_metadata.properties.description,data:data23};
if(vErrors === null){
vErrors = [err61];
}
else {
vErrors.push(err61);
}
errors++;
}
}
else {
const err62 = {instancePath:instancePath+"/ui_metadata/description",schemaPath:"#/properties/ui_metadata/properties/description/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.description.type,parentSchema:schema31.properties.ui_metadata.properties.description,data:data23};
if(vErrors === null){
vErrors = [err62];
}
else {
vErrors.push(err62);
}
errors++;
}
}
if(data21.order !== undefined){
let data24 = data21.order;
if(Array.isArray(data24)){
const len3 = data24.length;
for(let i4=0; i4<len3; i4++){
let data25 = data24[i4];
if(typeof data25 !== "string"){
const err63 = {instancePath:instancePath+"/ui_metadata/order/" + i4,schemaPath:"#/properties/ui_metadata/properties/order/items/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.order.items.type,parentSchema:schema31.properties.ui_metadata.properties.order.items,data:data25};
if(vErrors === null){
vErrors = [err63];
}
else {
vErrors.push(err63);
}
errors++;
}
}
let i5 = data24.length;
let j1;
if(i5 > 1){
const indices1 = {};
for(;i5--;){
let item1 = data24[i5];
if(typeof item1 !== "string"){
continue;
}
if(typeof indices1[item1] == "number"){
j1 = indices1[item1];
const err64 = {instancePath:instancePath+"/ui_metadata/order",schemaPath:"#/properties/ui_metadata/properties/order/uniqueItems",keyword:"uniqueItems",params:{i: i5, j: j1},message:"must NOT have duplicate items (items ## "+j1+" and "+i5+" are identical)",schema:true,parentSchema:schema31.properties.ui_metadata.properties.order,data:data24};
if(vErrors === null){
vErrors = [err64];
}
else {
vErrors.push(err64);
}
errors++;
break;
}
indices1[item1] = i5;
}
}
}
else {
const err65 = {instancePath:instancePath+"/ui_metadata/order",schemaPath:"#/properties/ui_metadata/properties/order/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.ui_metadata.properties.order.type,parentSchema:schema31.properties.ui_metadata.properties.order,data:data24};
if(vErrors === null){
vErrors = [err65];
}
else {
vErrors.push(err65);
}
errors++;
}
}
if(data21.groups !== undefined){
let data26 = data21.groups;
if(data26 && typeof data26 == "object" && !Array.isArray(data26)){
for(const key4 in data26){
let data27 = data26[key4];
if(data27 && typeof data27 == "object" && !Array.isArray(data27)){
if(data27.title !== undefined){
let data28 = data27.title;
if(typeof data28 !== "string"){
const err66 = {instancePath:instancePath+"/ui_metadata/groups/" + key4.replace(/~/g, "~0").replace(/\//g, "~1")+"/title",schemaPath:"#/properties/ui_metadata/properties/groups/additionalProperties/properties/title/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.title.type,parentSchema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.title,data:data28};
if(vErrors === null){
vErrors = [err66];
}
else {
vErrors.push(err66);
}
errors++;
}
}
if(data27.description !== undefined){
let data29 = data27.description;
if(typeof data29 !== "string"){
const err67 = {instancePath:instancePath+"/ui_metadata/groups/" + key4.replace(/~/g, "~0").replace(/\//g, "~1")+"/description",schemaPath:"#/properties/ui_metadata/properties/groups/additionalProperties/properties/description/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.description.type,parentSchema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.description,data:data29};
if(vErrors === null){
vErrors = [err67];
}
else {
vErrors.push(err67);
}
errors++;
}
}
if(data27.order !== undefined){
let data30 = data27.order;
if(!((typeof data30 == "number") && (!(data30 % 1) && !isNaN(data30)))){
const err68 = {instancePath:instancePath+"/ui_metadata/groups/" + key4.replace(/~/g, "~0").replace(/\//g, "~1")+"/order",schemaPath:"#/properties/ui_metadata/properties/groups/additionalProperties/properties/order/type",keyword:"type",params:{type: "integer"},message:"must be integer",schema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.order.type,parentSchema:schema31.properties.ui_metadata.properties.groups.additionalProperties.properties.order,data:data30};
if(vErrors === null){
vErrors = [err68];
}
else {
vErrors.push(err68);
}
errors++;
}
}
}
else {
const err69 = {instancePath:instancePath+"/ui_metadata/groups/" + key4.replace(/~/g, "~0").replace(/\//g, "~1"),schemaPath:"#/properties/ui_metadata/properties/groups/additionalProperties/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.groups.additionalProperties.type,parentSchema:schema31.properties.ui_metadata.properties.groups.additionalProperties,data:data27};
if(vErrors === null){
vErrors = [err69];
}
else {
vErrors.push(err69);
}
errors++;
}
}
}
else {
const err70 = {instancePath:instancePath+"/ui_metadata/groups",schemaPath:"#/properties/ui_metadata/properties/groups/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.groups.type,parentSchema:schema31.properties.ui_metadata.properties.groups,data:data26};
if(vErrors === null){
vErrors = [err70];
}
else {
vErrors.push(err70);
}
errors++;
}
}
if(data21.fields !== undefined){
let data31 = data21.fields;
if(data31 && typeof data31 == "object" && !Array.isArray(data31)){
for(const key5 in data31){
let data32 = data31[key5];
if(data32 && typeof data32 == "object" && !Array.isArray(data32)){
if(data32.title !== undefined){
let data33 = data32.title;
if(typeof data33 !== "string"){
const err71 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/title",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/title/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.title.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.title,data:data33};
if(vErrors === null){
vErrors = [err71];
}
else {
vErrors.push(err71);
}
errors++;
}
}
if(data32.description !== undefined){
let data34 = data32.description;
if(typeof data34 !== "string"){
const err72 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/description",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/description/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.description.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.description,data:data34};
if(vErrors === null){
vErrors = [err72];
}
else {
vErrors.push(err72);
}
errors++;
}
}
if(data32.group !== undefined){
let data35 = data32.group;
if(typeof data35 !== "string"){
const err73 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/group",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/group/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.group.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.group,data:data35};
if(vErrors === null){
vErrors = [err73];
}
else {
vErrors.push(err73);
}
errors++;
}
}
if(data32.order !== undefined){
let data36 = data32.order;
if(!((typeof data36 == "number") && (!(data36 % 1) && !isNaN(data36)))){
const err74 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/order",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/order/type",keyword:"type",params:{type: "integer"},message:"must be integer",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.order.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.order,data:data36};
if(vErrors === null){
vErrors = [err74];
}
else {
vErrors.push(err74);
}
errors++;
}
}
if(data32.advanced !== undefined){
let data37 = data32.advanced;
if(typeof data37 !== "boolean"){
const err75 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/advanced",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/advanced/type",keyword:"type",params:{type: "boolean"},message:"must be boolean",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.advanced.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.advanced,data:data37};
if(vErrors === null){
vErrors = [err75];
}
else {
vErrors.push(err75);
}
errors++;
}
}
if(data32.sensitive !== undefined){
let data38 = data32.sensitive;
if(typeof data38 !== "boolean"){
const err76 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/sensitive",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/sensitive/type",keyword:"type",params:{type: "boolean"},message:"must be boolean",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.sensitive.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.sensitive,data:data38};
if(vErrors === null){
vErrors = [err76];
}
else {
vErrors.push(err76);
}
errors++;
}
}
if(data32.readOnly !== undefined){
let data39 = data32.readOnly;
if(typeof data39 !== "boolean"){
const err77 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/readOnly",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/readOnly/type",keyword:"type",params:{type: "boolean"},message:"must be boolean",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.readOnly.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.readOnly,data:data39};
if(vErrors === null){
vErrors = [err77];
}
else {
vErrors.push(err77);
}
errors++;
}
}
if(data32.computed !== undefined){
let data40 = data32.computed;
if(typeof data40 !== "boolean"){
const err78 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/computed",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/computed/type",keyword:"type",params:{type: "boolean"},message:"must be boolean",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.computed.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.computed,data:data40};
if(vErrors === null){
vErrors = [err78];
}
else {
vErrors.push(err78);
}
errors++;
}
}
if(data32.enum !== undefined){
let data41 = data32.enum;
if(!(Array.isArray(data41))){
const err79 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/enum",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/enum/type",keyword:"type",params:{type: "array"},message:"must be array",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.enum.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.enum,data:data41};
if(vErrors === null){
vErrors = [err79];
}
else {
vErrors.push(err79);
}
errors++;
}
}
if(data32.visibleWhen !== undefined){
let data42 = data32.visibleWhen;
if(data42 && typeof data42 == "object" && !Array.isArray(data42)){
}
else {
const err80 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/visibleWhen",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/visibleWhen/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.visibleWhen.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.visibleWhen,data:data42};
if(vErrors === null){
vErrors = [err80];
}
else {
vErrors.push(err80);
}
errors++;
}
}
if(data32.items !== undefined){
let data43 = data32.items;
if(data43 && typeof data43 == "object" && !Array.isArray(data43)){
}
else {
const err81 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1")+"/items",schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/properties/items/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.items.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties.properties.items,data:data43};
if(vErrors === null){
vErrors = [err81];
}
else {
vErrors.push(err81);
}
errors++;
}
}
}
else {
const err82 = {instancePath:instancePath+"/ui_metadata/fields/" + key5.replace(/~/g, "~0").replace(/\//g, "~1"),schemaPath:"#/properties/ui_metadata/properties/fields/additionalProperties/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.fields.additionalProperties.type,parentSchema:schema31.properties.ui_metadata.properties.fields.additionalProperties,data:data32};
if(vErrors === null){
vErrors = [err82];
}
else {
vErrors.push(err82);
}
errors++;
}
}
}
else {
const err83 = {instancePath:instancePath+"/ui_metadata/fields",schemaPath:"#/properties/ui_metadata/properties/fields/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.properties.fields.type,parentSchema:schema31.properties.ui_metadata.properties.fields,data:data31};
if(vErrors === null){
vErrors = [err83];
}
else {
vErrors.push(err83);
}
errors++;
}
}
}
else {
const err84 = {instancePath:instancePath+"/ui_metadata",schemaPath:"#/properties/ui_metadata/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.properties.ui_metadata.type,parentSchema:schema31.properties.ui_metadata,data:data21};
if(vErrors === null){
vErrors = [err84];
}
else {
vErrors.push(err84);
}
errors++;
}
}
if(data.package_hash !== undefined){
let data44 = data.package_hash;
if(typeof data44 === "string"){
if(!pattern9.test(data44)){
const err85 = {instancePath:instancePath+"/package_hash",schemaPath:"#/properties/package_hash/pattern",keyword:"pattern",params:{pattern: "^[0-9a-f]{64}$"},message:"must match pattern \""+"^[0-9a-f]{64}$"+"\"",schema:"^[0-9a-f]{64}$",parentSchema:schema31.properties.package_hash,data:data44};
if(vErrors === null){
vErrors = [err85];
}
else {
vErrors.push(err85);
}
errors++;
}
}
else {
const err86 = {instancePath:instancePath+"/package_hash",schemaPath:"#/properties/package_hash/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.package_hash.type,parentSchema:schema31.properties.package_hash,data:data44};
if(vErrors === null){
vErrors = [err86];
}
else {
vErrors.push(err86);
}
errors++;
}
}
if(data.semantic_hash !== undefined){
let data45 = data.semantic_hash;
if(typeof data45 === "string"){
if(!pattern9.test(data45)){
const err87 = {instancePath:instancePath+"/semantic_hash",schemaPath:"#/properties/semantic_hash/pattern",keyword:"pattern",params:{pattern: "^[0-9a-f]{64}$"},message:"must match pattern \""+"^[0-9a-f]{64}$"+"\"",schema:"^[0-9a-f]{64}$",parentSchema:schema31.properties.semantic_hash,data:data45};
if(vErrors === null){
vErrors = [err87];
}
else {
vErrors.push(err87);
}
errors++;
}
}
else {
const err88 = {instancePath:instancePath+"/semantic_hash",schemaPath:"#/properties/semantic_hash/type",keyword:"type",params:{type: "string"},message:"must be string",schema:schema31.properties.semantic_hash.type,parentSchema:schema31.properties.semantic_hash,data:data45};
if(vErrors === null){
vErrors = [err88];
}
else {
vErrors.push(err88);
}
errors++;
}
}
}
else {
const err89 = {instancePath,schemaPath:"#/type",keyword:"type",params:{type: "object"},message:"must be object",schema:schema31.type,parentSchema:schema31,data};
if(vErrors === null){
vErrors = [err89];
}
else {
vErrors.push(err89);
}
errors++;
}
validate20.errors = vErrors;
return errors === 0;
}
validate20.evaluated = {"props":true,"dynamicProps":false,"dynamicItems":false};
