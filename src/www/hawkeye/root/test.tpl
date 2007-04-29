<html>
<body>
<h1>Test Template</h1>

<p>This is designed to exercise the Bongo Template system.</p>

<p>Templates are first pre-processed, then run entire result is run through the Tal system.</p>

<table border="1">
<tr>
	<th>Test</th>
	<th>Output</th>
</tr>
<tr>
	<td>PP substitution</td>
	<td>%(helloworld)s</td>
</tr>
<tr>	
	<td>PP relative URL fixup</td>
	<td>%(url|/)s</td>
</tr>
<tr>
	<td>PP include template</td>
	<td>%(include|root/test-hello.tpl)s</td>
</tr>
<tr>
	<td>Tal substitution</td>
	<td tal:content="helloworld">Test failed.</td>
</tr>
<tr>
	<td>Tal sub. w/restructure</td>
	<td>
	<p tal:content="text escapeme">Test 1 failed.</p>
	<p tal:content="structure escapeme">Test 2 failed.</p>
	</td>
</tr>
<tr>
	<td>Tal loopy</td>
	<!-- repeat="name expression" -->
	<td><ul tal:repeat="list testlist">
		<li tal:content="list/say"></li>
	</ul>
	</td>
</tr>
<tr>
	<td>Tal conditionals</td>
	<td>
	<p tal:condition="somethingtrue">Test 1 succeeded!</p>
	<p tal:condition="somethinguntrue">Test 2 FAILED</p>
	</td>
</tr>
<tr>
	<td>Tal attributes</td>
	<td><p tal:attributes="style greenstyle" style="color: red">If I'm green, the test succeeded!</p></td>
</tr>
<tr>
	<td>Tal omit-tag</td>
	<td><p style="color:red" tal:omit-tag="somethingtrue">If I'm red, the test failed.</p></td>
</tr>
</table>

</body>
</html>
