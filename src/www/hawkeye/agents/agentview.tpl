%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)sagents">Agents</a> &#187 <a href="#" tal:content="hname">Agent</a></p></div>

<h1 tal:content="hname">Agent</h1>

<div class="info" id="msg" tal:condition="info" tal:content="info">Something happened, but I'm not sure.</div>
<div class="error" id="err" tal:condition="error" tal:content="error">An error occured while processing your request. No more information is available.</div>
<div class="info" id="opsuccess" tal:condition="opsuccess">Operation completed sucessfully.</div>

<p>
<form method="post">
<table cellpadding="1">

<tr tal:repeat="setting settinglist">
    <td style="padding-right: 16px;"><label tal:attributes="for setting/id" tal:content="setting/name"></label></td>
    <td tal:condition="setting/textentry">
        <input type="text" tal:attributes="name setting/id;id setting/id;value setting/value" /> <span tal:content="setting/suffix"></span>
    </td>
    
    <td tal:condition="setting/numericentry">
        <input type="text" style="width: 45px;" tal:attributes="name setting/id;id setting/id;value setting/value" /> <span tal:content="setting/suffix"></span>
    </td>
    
    <td tal:condition="setting/boolentry">
        <input type="checkbox" tal:attributes="name setting/id;id setting/id;checked setting/checked" /> <span tal:content="setting/suffix"></span>
    </td>
    
    <input type="hidden" tal:condition="setting/hidden" tal:attributes="name setting/id;id setting/id;value setting/value" />
</tr>
</table>
<br />
<br />
<span class="button"><button type="reset" value="Reset">Reset</button></span> <span class="button"><button type="submit" value="Save">Save</button></span>
</form>
</p>
%(include|footer.tpl)s
