package scorer

import (
	"gopkg.in/yaml.v2"
	"regexp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/klog"
)

const (
	// ConfigMapKey defines the key used in the ConfigMap to hint priorities
	bonusKey = "bonus"
	malusKey = "malus"
	prioKey  = "priorities"
)

type Hints struct {
	bonus, malus map[int][]*regexp.Regexp
	priorities   map[int][]string
}

func parseHintsString(yamlString string) (hints map[int][]*regexp.Regexp) {
	var res map[int][]string
	hints = make(map[int][]*regexp.Regexp)
	if err := yaml.Unmarshal([]byte(yamlString), &res); err != nil {
		klog.Errorf("Can't parse YAML with hints: %v - error: %v", yamlString, err)
		return
	}
	for prio, reList := range res {
		for _, re := range reList {
			regexp, err := regexp.Compile(re)
			if err != nil {
				klog.Errorf("Can't compile regexp rule for priority %d and rule %s: %v", prio, re, err)
				return
			}
			hints[prio] = append(hints[prio], regexp)
		}
	}
	return
}

func (s *Scorer) getOrCreateHints() error {
	var bonusString, malusString, prioString string
	var found bool
	needsUpdate := false

	cm, err := s.cmLister.ConfigMaps(s.namespace).Get(s.config.HintsConfigMapName)
	if err == nil {
		bonusString, found = cm.Data[bonusKey]
		if !found {
			bonusString = "{}"
			cm.Data[bonusKey] = bonusString
			needsUpdate = true
		}
		s.hints.bonus = parseHintsString(bonusString)

		malusString, found = cm.Data[malusKey]
		if !found {
			malusString = "{}"
			cm.Data[malusKey] = malusString
			needsUpdate = true
		}
		s.hints.malus = parseHintsString(malusString)

		prioString, found = cm.Data[prioKey]
		if !found {
			prioString = "{}"
			cm.Data[prioKey] = prioString
			needsUpdate = true
		}
		if err := yaml.Unmarshal([]byte(prioString), &s.hints.priorities); err != nil {
			klog.Errorf("Can't parse YAML with hinted priorities in the configmap: %v", err)
		}
	} else {
		statusErr, ok := err.(*errors.StatusError)
		if !ok {
			klog.Errorf("Error getting %s/%s config map: %v", s.namespace, s.config.HintsConfigMapName, err)
			return err
		}
		if statusErr.Status().Reason == metav1.StatusReasonNotFound {
			_, err := s.clientset.CoreV1().ConfigMaps(s.namespace).
				Create(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: s.namespace,
						Name:      s.config.HintsConfigMapName,
					},
					Data: map[string]string{
						"bonus":      "{}",
						"malus":      "{}",
						"priorities": "{}",
					},
				})
			if err != nil {
				klog.Errorf("Error creating %s/%s config map: %v", s.namespace, s.config.HintsConfigMapName, err)
				return err
			}
			return nil
		} else {
			klog.Errorf("Error getting %s/%s config map: %v", s.namespace, s.config.HintsConfigMapName, err)
			return err
		}
	}

	if needsUpdate {
		if _, err = s.clientset.CoreV1().ConfigMaps(cm.ObjectMeta.Namespace).Update(cm); err != nil {
			klog.Errorf("Error updating %s/%s config map: %v", cm.ObjectMeta.Namespace, cm.ObjectMeta.Name, err)
			return err
		}
	}
	return nil
}
