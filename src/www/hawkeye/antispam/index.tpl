%(include|header.tpl)s

<form method="post">
<table cellpadding="1">
<tr><td style="padding-right: 16px;"><label for="timeout">Timeout: </label></td><td><input type="text" name="timeout" id="timeout" tal:attributes="value timeout" /> milliseconds</td></tr>
<tr><td><label for="host">Host: </label></td><td><input type="text" name="host" id="host" tal:attributes="value host" />:<input type="text" name="port" tal:attributes="value port" style="width: 45px;" /></td></tr>
</table>
<br />
<br />
<span class="button"><button type="submit" value="Save">Save</button></span>
</form>

%(include|footer.tpl)s
