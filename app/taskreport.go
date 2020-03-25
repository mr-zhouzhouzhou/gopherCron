package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/holdno/gopherCron/common"
	"github.com/holdno/gopherCron/config"
	"github.com/holdno/gopherCron/pkg/selection"
	"github.com/holdno/gopherCron/pkg/store"
	"github.com/holdno/gopherCron/pkg/store/sqlStore"

	"github.com/sirupsen/logrus"
)

type HttpTaskResultReporter struct {
	hc            *http.Client
	reportAddress string
}

func NewHttpTaskResultReporter(address string) *HttpTaskResultReporter {
	return &HttpTaskResultReporter{
		hc: &http.Client{
			Timeout: 5 * time.Second,
		},
		reportAddress: address,
	}
}

func (r *HttpTaskResultReporter) ResultReport(result *common.TaskExecuteResult) error {
	if result == nil {
		return nil
	}
	b, _ := json.Marshal(result)
	req, _ := http.NewRequest(http.MethodPost, r.reportAddress, bytes.NewReader(b))
	req.Header.Add("content-type", "application/json")

	resp, err := r.hc.Do(req)
	if err != nil {
		return fmt.Errorf("failed to post task result, %w", err)
	}

	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("task result report failed, log service status error, response status: %d, content: %s",
			resp.StatusCode, string(body))
	}

	return nil
}

type ClientTaskReporter interface {
	ResultReport(result *common.TaskExecuteResult) error
}

type TaskResultReporter struct {
	logger       *logrus.Logger
	taskLogStore store.TaskLogStore
	projectStore store.ProjectStore
}

func NewDefaultTaskReporter(logger *logrus.Logger, mysqlConf *config.MysqlConf) ClientTaskReporter {
	_store := sqlStore.MustSetup(mysqlConf, logger, false)
	return &TaskResultReporter{
		logger:       logger,
		taskLogStore: _store.TaskLog(),
		projectStore: _store.Project(),
	}
}

func (r *TaskResultReporter) ResultReport(result *common.TaskExecuteResult) error {
	if result == nil {
		return errors.New("failed to report task result, empty result")
	}

	var (
		resultBytes    []byte
		projects       []*common.Project
		projectInfo    *common.Project
		err            error
		getError       int
		logInfo        common.TaskLog
		taskResult     *common.TaskResultLog
		jsonMarshalErr error
	)

	opts := selection.NewSelector(selection.NewRequirement("id", selection.Equals, result.ExecuteInfo.Task.ProjectID))
	if projects, err = r.projectStore.GetProject(opts); err != nil {
		return fmt.Errorf("failed to report task result, the task project not found, %w", err)
	}

	if len(projects) > 0 {
		projectInfo = projects[0]
	} else {
		r.logger.WithField("project_id", result.ExecuteInfo.Task.ProjectID).Errorf("task result report error, project not exist!")
		return errors.New("task result report error, project not exist!")
	}

	taskResult = &common.TaskResultLog{
		Result: result.Output,
	}

	if result.Err != nil {
		taskResult.SystemError = result.Err.Error()
		getError = 1
	}

	if resultBytes, jsonMarshalErr = json.Marshal(taskResult); jsonMarshalErr != nil {
		resultBytes = []byte("result log json marshal error:" + jsonMarshalErr.Error())
	}

	logInfo = common.TaskLog{
		Name:      result.ExecuteInfo.Task.Name,
		Result:    string(resultBytes),
		StartTime: result.StartTime.Unix(),
		EndTime:   result.EndTime.Unix(),
		Command:   result.ExecuteInfo.Task.Command,
		WithError: getError,
		ClientIP:  result.ExecuteInfo.Task.ClientIP,
	}

	if projectInfo != nil {
		logInfo.Project = projectInfo.Title
	}

	logInfo.ProjectID = result.ExecuteInfo.Task.ProjectID
	logInfo.TaskID = result.ExecuteInfo.Task.TaskID

	if err = r.taskLogStore.CreateTaskLog(logInfo); err != nil {
		r.logger.WithFields(logrus.Fields{
			"task_name":  logInfo.Name,
			"result":     logInfo.Result,
			"error":      err.Error(),
			"start_time": time.Unix(logInfo.StartTime, 0).Format("2006-01-02 15:05:05"),
			"end_time":   time.Unix(logInfo.StartTime, 0).Format("2006-01-02 15:05:05"),
		}).Error("任务日志入库失败")

		return fmt.Errorf("failed to save task result, %w", err)
	}
	return nil
}
