package main

// Reading two SQL Servers for a plan: the instances, each database's
// recommended method and what the instance objects would do. Only SELECT
// queries and the read-only extended procedures in sqlInfoBatch run here.

import (
	"context"
	"fmt"
	"strings"
)

type sqlExecFunc func(ctx context.Context, plan *sqlPlan, batches []sqlBatch) ([]sqlBatchResult, error)

// sqlReadSide reads one instance.
func sqlReadSide(ctx context.Context, exec sqlExecFunc, plan *sqlPlan, on string) (*sqlInstance, error) {
	server := plan.Source.Server
	if on == sqlDst {
		server = plan.Target.Server
	}
	res, err := exec(ctx, plan, []sqlBatch{sqlInfoBatch(on)})
	if err != nil {
		return nil, err
	}
	info := sqlResult(res, "info")
	if info == nil || !info.OK {
		text := "no answer"
		if info != nil {
			text = info.Error
		}
		return nil, fmt.Errorf("%s: %s", server, text)
	}
	inst := &sqlInstance{Server: server}
	if err := sqlParseInfo(inst, info.rows()); err != nil {
		return nil, err
	}
	res, err = exec(ctx, plan, sqlDetailBatches(on, inst.Major, inst.Hadr))
	if err != nil {
		return nil, err
	}
	rows := func(name string) []sqlRow {
		if x := sqlResult(res, name); x != nil && x.OK {
			return x.rows()
		}
		return nil
	}
	if x := sqlResult(res, "databases"); x == nil || !x.OK {
		text := "the databases could not be read"
		if x != nil && x.Error != "" {
			text += ": " + x.Error
		}
		return nil, fmt.Errorf("%s: %s", server, text)
	}
	sqlParseDatabases(inst, rows("databases"), rows("files"), rows("volumes"), rows("ags"), rows("endpoint"))
	return inst, nil
}

// sqlReadView is what the page gets after Read servers.
type sqlReadView struct {
	Source   *sqlInstance           `json:"source"`
	Target   *sqlInstance           `json:"target"`
	Choices  map[string]sqlChoice   `json:"choices"`
	Objects  []sqlObjectItem        `json:"objects"`
	Config   []sqlConfigDiff        `json:"config"`
	Counts   map[string]int         `json:"counts"`
	Findings []sqlFinding           `json:"findings"`
	Suggest  sqlSuggestion          `json:"suggest"`
	Errors   map[string]string      `json:"errors,omitempty"`
	Reasons  map[string]sqlDBReason `json:"reasons"`
}

// sqlDBReason says why a database cannot be picked, or "".
type sqlDBReason struct {
	Off string `json:"off,omitempty"`
}

// sqlSuggestion is what the page fills in, each with its reason.
type sqlSuggestion struct {
	BackupPath    string `json:"backupPath"`
	BackupWhy     string `json:"backupWhy"`
	DataPath      string `json:"dataPath"`
	LogPath       string `json:"logPath"`
	PathWhy       string `json:"pathWhy"`
	SyncMinutes   int    `json:"syncMinutes"`
	SyncWhy       string `json:"syncWhy"`
	Concurrency   int    `json:"concurrency"`
	AliasZone     string `json:"aliasZone"`
	AliasWhy      string `json:"aliasWhy"`
	ObjectsWhy    string `json:"objectsWhy"`
	DisableJobs   bool   `json:"disableJobs"`
	DisableJobWhy string `json:"disableJobWhy"`
}

func sqlBuildReadView(src, dst *sqlInstance, srcObj, dstObj *sqlObjects, objErr error) sqlReadView {
	v := sqlReadView{Source: src, Target: dst, Choices: map[string]sqlChoice{}, Counts: map[string]int{}, Reasons: map[string]sqlDBReason{}, Errors: map[string]string{}}
	for i := range src.Databases {
		db := &src.Databases[i]
		if why, system := sqlSystemDatabases[strings.ToLower(db.Name)]; system {
			if why == "" {
				why = "System database: its logins, jobs and linked servers move as instance objects."
			}
			v.Reasons[db.Name] = sqlDBReason{Off: why}
			continue
		}
		if !strings.EqualFold(db.State, "ONLINE") {
			v.Reasons[db.Name] = sqlDBReason{Off: "It is " + strings.ToLower(db.State) + "."}
			continue
		}
		if t := dst.database(db.Name); t != nil {
			v.Reasons[db.Name] = sqlDBReason{Off: "The new server already has a database with this name; give the copy another name to move it."}
		}
		v.Choices[db.Name] = sqlRecommend(src, dst, db)
	}
	if objErr != nil {
		v.Errors["objects"] = objErr.Error()
	} else if srcObj != nil && dstObj != nil {
		all := sqlObjectChoice{Logins: true, Jobs: true, LinkedServers: true, Operators: true}
		_, items := sqlObjectPlan(*srcObj, *dstObj, src, dst, all, map[string]string{})
		v.Objects = items
		v.Config = sqlConfigDiffs(*srcObj, *dstObj)
		v.Counts["logins"] = len(srcObj.Logins)
		v.Counts["jobs"] = len(srcObj.Jobs)
		v.Counts["linked"] = len(srcObj.Linked)
		v.Counts["operators"] = len(srcObj.Operators)
		if !srcObj.AgentRead {
			v.Errors["jobs"] = "SQL Server Agent jobs could not be read on the old server (Express edition, or no access to msdb)."
		}
	}
	v.Suggest = sqlSuggest(src, dst, srcObj)
	return v
}

// sqlSuggest fills the plan's settings from the two servers.
func sqlSuggest(src, dst *sqlInstance, srcObj *sqlObjects) sqlSuggestion {
	s := sqlSuggestion{SyncMinutes: 15, Concurrency: 2, DataPath: dst.DataPath, LogPath: dst.LogPath,
		SyncWhy:    "Every 15 minutes keeps the copy close with little load; the cutover ships only what changed since.",
		PathWhy:    "The new server's default data and log folders.",
		ObjectsWhy: "Logins keep their SIDs and passwords, so database users and applications sign in unchanged. Jobs are created turned off and turned on at cutover."}
	if src.BackupPath != "" && src.Machine != "" && len(src.BackupPath) > 2 && src.BackupPath[1] == ':' {
		s.BackupPath = `\\` + src.Machine + `\` + string(src.BackupPath[0]) + `$` + src.BackupPath[2:] + `\SIMM`
		s.BackupWhy = "The old server's own backup folder through its administrative share. Both SQL Server service accounts need access to it; Check backup folder tests that. A share on a file server works too."
	} else if strings.HasPrefix(src.BackupPath, `\\`) {
		s.BackupPath = strings.TrimRight(src.BackupPath, `\`) + `\SIMM`
		s.BackupWhy = "The old server's backup share."
	} else {
		s.BackupWhy = "Type a share both SQL Server service accounts can reach (\\\\fileserver\\share\\folder)."
	}
	if dst.DNSDomain != "" {
		s.AliasZone = dst.DNSDomain
		s.AliasWhy = "The new server's DNS domain."
	}
	if srcObj != nil && len(srcObj.Jobs) > 0 {
		s.DisableJobs = true
		s.DisableJobWhy = "Turning the old server's jobs off at cutover keeps a job from running against the old copy too. Roll back turns them on again."
	}
	return s
}

// sqlCheckFolderBatches create the plan folder from the old server and
// check the new server sees it.
func sqlCheckFolderBatches(folder string) []sqlBatch {
	probe := folder + `\SIMM-check`
	return []sqlBatch{sqlCreateFolderBatch(sqlOn, probe),
		{On: sqlDst, Name: "folder visible", Kind: "folderexists", Timeout: 60, Args: map[string]string{"path": probe},
			SQL: "SET NOCOUNT ON; CREATE TABLE #f (e int, d int, p int); INSERT #f EXEC master.dbo.xp_fileexist " + sqlStr(probe) + "; SELECT d AS isFolder FROM #f; DROP TABLE #f;"}}
}

func sqlCheckFolderResult(res []sqlBatchResult, folder string, src, dst sqlBrief) error {
	if x := sqlResult(res, "folder"); x == nil || !x.OK {
		text := ""
		if x != nil {
			text = ": " + x.Error
		}
		return fmt.Errorf("the old server's SQL Server service (%s) could not create a folder in %s%s", sqlOr(src.ServiceAccount, "its service account"), folder, text)
	}
	x := sqlResult(res, "folder visible")
	if x == nil || !x.OK || len(x.rows()) == 0 || !x.rows()[0].flag("isFolder") {
		return fmt.Errorf("the new server's SQL Server service (%s) cannot see %s. Give it read access to that share and folder", sqlOr(dst.ServiceAccount, "its service account"), folder)
	}
	return nil
}
