%(include|header.tpl)s

<img src="/img/desktop-big.png" class="floaty" alt="Welcome to Bongo" />

<p>Thank you for choosing Bongo as your calendaring and mail solution.</p>
<p>This screen enables you to briefly overview your system status, and as a starting point to the different administration tasks available.</p>
<p>Help can be accessed by clicking on the help menu at any time.<br />To get started, click on one of the menu items to your left.</p>

<br />

<table class="htable" width="100%%" cellspacing="0" >
    <tr style="height: 28px;" class="highlight">
        <td class="hrow">Agent Status</td>
        <td style="text-align: center;">Operation normal.</td>
        <td class="hrow" width="16"></td>
    </tr>
    <tr class="highlight1">
        <td class="hrow">Bongo Updates</td>
        <td style="text-align: center;">
	  Running version: <span tal:content="sw_current">unknown</span> <br />
	  Latest available: <span tal:content="sw_available">unknown</span>
	  <div tal:condition="sw_upgrade">Upgrade to new version recommended.</div>
	</td>
        <td class="hrow" width="16"><img src="/img/dialog-warning.png" alt="Warning" tal:condition="sw_upgrade" /></td>
    </tr>
    <tr style="height: 28px;" class="highlight">
        <td class="hrow">System Status</td>
        <td style="text-align: center;">Current processing load: <span tal:content="load">unknown</span></td>
        <td class="hrow" width="16"></td>
    </tr>
    <tr style="height: 28px;" class="highlight1">
        <td class="hrow">Memory Usage</td>
        <td style="text-align: center;" tal:content="mem">RAM</td>
        <td class="hrow" width="16"></td>
    </tr>
    <tr style="height: 28px;" class="highlight">
        <td class="hrow">Load</td>
        <td style="text-align: center;">Unknown</td>
        <td class="hrow" width="16"></td>
    </tr>
</table>


%(include|footer.tpl)s
