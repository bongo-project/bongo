%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)sagents">Agents</a></p></div>

<h1>Agents</h1>

<div class="info" id="msg" tal:condition="info" tal:content="info">Something happened, but I'm not sure.</div>
<div class="error" id="err" tal:condition="error" tal:content="error">An error occured while processing your request. No more information is available.</div>
<div class="info" id="opsuccess" tal:condition="opsuccess">Operation completed sucessfully.</div>

<ul>
<li><a href="%(url|/)sserver/">Enable/Disable Agents</a></li>
<li><a href="%(url|/)santispam/">Antispam</a></li>
</ul>

%(include|footer.tpl)s
