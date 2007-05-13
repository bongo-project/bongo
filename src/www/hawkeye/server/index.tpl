%(include|header.tpl)s

<p>

<div tal:condition="success" style="text-align: center;">
<form method="post">
	<input type="hidden" name="command" value="agentstatus" />
	<table class="htable" cellspacing="0">
	<tr class="highlight hrow">
		<th style="padding: 10px;">Enabled</th>
		<th style="padding: 10px;">Agent Name</th>
	</tr>
	<tr tal:repeat="agent agentlist" tal:attributes="class string:highlight${repeat/agent/odd}">
		<td style="height: 24px">
		<input type="checkbox" tal:attributes="checked agent/enabled;name agent/name" />
		</td>
		<td tal:content="agent/name">unknown agent</td>
	</tr>
	</table>
	<br />
	<input type="submit" value="Save" />
</form>
</div>

</p>

%(include|footer.tpl)s
