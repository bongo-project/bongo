%(include|header.tpl)s

<h1>Enable/Disable Agents</h1>

<p tal:condition="message" tal:content="message">No message.</p>

<p tal:condition="not:success">
	Unable to read configuration information from the Bongo store.
	Are you logged in as a user with administrative permissions?
</p>

<div tal:condition="success">
<form method="post">
	<input type="hidden" name="command" value="agentstatus" />
	<table class="htable">
	<tr>
		<th>Enabled?</th>
		<th>Agent Name</th>
	</tr>
	<tr tal:repeat="agent agentlist">
		<td>
		<input type="checkbox" tal:attributes="checked agent/enabled;name agent/name" />
		</td>
		<td tal:content="agent/name">unknown agent</td>
	</tr>
	</table>
	<input type="submit" value="Save" />
</form>
</div>

%(include|footer.tpl)s
