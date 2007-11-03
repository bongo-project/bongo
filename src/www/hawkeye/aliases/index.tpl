%(include|header.tpl)s

<div class="breadcrumb"><p><a href="%(url|/)ssystem">System</a> &#187 <a href="%(url|/)saliases">Aliases</a></p></div>

<h1>Domains</h1>

<a href="%(url|/)saliases/add"><img src="/img/add-domain.png" alt="Add" border="0" /></a>
<br />
<p>
<table class="htable" width="100%%" cellspacing="0" >
    <tr tal:repeat="domain domainlist" tal:attributes="class string:highlight${repeat/domain/odd}">
		<td style="height: 24px">
		    <span style="float: left; padding-left: 1em"><a tal:attributes="href domain/editlink" style="color: #fff"><span tal:content="domain/name"></span></a></span><span style="float: right;"><a tal:attributes="href domain/editlink"><img src="/img/document-properties.png" alt="Properties" border="0" /></a><a tal:attributes="href domain/deletelink"><img src="/img/document-delete.png" alt="Delete" border="0" /></a></span>
		</td>
		<td tal:content="agent/name">unknown agent</td>
	</tr>
</table>
</p>

%(include|footer.tpl)s
