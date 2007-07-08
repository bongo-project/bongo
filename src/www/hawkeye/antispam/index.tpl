%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)sagents">Agents</a> &#187 <a href="%(url|/)santispam/">Antispam</a></p></div>

<h1>Antispam</h1>

<div class="info" id="msg" tal:condition="info" tal:content="info">Something happened, but I'm not sure.</div>
<div class="error" id="err" tal:condition="error" tal:content="error">An error occured while processing your request. No more information is available.</div>
<div class="info" id="opsuccess" tal:condition="opsuccess">Operation completed sucessfully.</div>

<p>
<form method="post">
<table cellpadding="1">
<tr><td style="padding-right: 16px;"><label for="timeout">Timeout: </label></td><td><input type="text" name="timeout" id="timeout" tal:attributes="value timeout" /> milliseconds</td></tr>
<tr><td><label for="host">Host: </label></td><td><input type="text" name="host" id="host" tal:attributes="value host" />:<input type="text" name="port" tal:attributes="value port" style="width: 45px;" /></td></tr>
</table>
<br />
<br />
<span class="button"><button type="submit" value="Save">Save</button></span>
</form>
</p>
%(include|footer.tpl)s
